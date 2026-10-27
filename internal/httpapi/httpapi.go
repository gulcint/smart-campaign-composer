// Package httpapi is the HTTP router of cmd/api (docs/contracts/openapi.yaml).
//
// It serves the system endpoints (/healthz, /readyz, /metrics, /api/config), the
// interactive documentation (/docs, /openapi.yaml; docs.go), the audience slice (campaign
// create/get/list, segment generation, rule counting), the content + approval slice (SSE content
// generation, versions, edits, approval, preview; content.go) and the cross-cutting behaviour every
// route shares: X-Request-ID, the 64 KiB body limit, JSON 404/405 errors, error mapping, HTTP
// metrics and panic recovery. The delivery routes are added by the P5 slice.
package httpapi

import (
	"embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/health"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/metrics"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

type Deps struct {
	Service     campaign.Service // fakeable
	LLMProvider llm.Provider     // effective provider for GET /api/config and header gating
	DB          health.Pinger
	Metrics     http.Handler
	Clock       clock.Clock // all stream timing (poll, heartbeat) runs on this clock
	// StatusPollInterval: live-status stream poll period; zero => 1s.
	StatusPollInterval time.Duration
	// HeartbeatInterval: ": keepalive" comment period on both SSE streams; zero => 15s.
	HeartbeatInterval time.Duration
	Logger            *slog.Logger
}

// MaxBodyBytes is the request body limit (contracts README §3).
const MaxBodyBytes = 65536

// New returns the full router of openapi.yaml (including /healthz, /readyz, /metrics,
// body limit 64 KiB, X-Request-ID, error mapping).
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Clock == nil {
		d.Clock = clock.Real()
	}
	if d.StatusPollInterval == 0 {
		d.StatusPollInterval = time.Second
	}
	if d.HeartbeatInterval == 0 {
		d.HeartbeatInterval = 15 * time.Second
	}
	a := &api{deps: d}

	r := &router{}
	for _, e := range routeTable {
		r.handle(e.Template, e.Method, e.handler(a))
	}
	return a.middleware(r)
}

// Route is one method + path template the router registers. Template uses the OpenAPI
// `{name}` syntax exactly as the key under `paths:` in openapi.yaml; Method is upper case.
type Route struct {
	Method, Template string
}

// routeTable is the single source of the router: New registers exactly these entries and
// Routes() lists exactly these entries, so the two cannot drift (contracts README §11). A slice
// that adds an endpoint adds a row here and updates openapi.yaml in the same PR.
var routeTable = []struct {
	Route
	handler func(*api) http.Handler
}{
	{Route{http.MethodGet, "/healthz"}, func(*api) http.Handler { return health.Healthz() }},
	{Route{http.MethodGet, "/readyz"}, func(a *api) http.Handler { return health.Readyz(a.deps.DB) }},
	{Route{http.MethodGet, "/metrics"}, (*api).metricsHandler},
	{Route{http.MethodGet, "/api/config"}, handlerOf((*api).getConfig)},

	// Interactive documentation (tag `documentation`; docs.go).
	{Route{http.MethodGet, "/docs"}, handlerOf((*api).getAPIDocs)},
	{Route{http.MethodGet, "/docs/{asset}"}, handlerOf((*api).getAPIDocsAsset)},
	{Route{http.MethodGet, "/openapi.yaml"}, handlerOf((*api).getOpenAPISpec)},
	{Route{http.MethodGet, "/schemas/{schema}"}, handlerOf((*api).getContractSchema)},

	// Audience slice (P3).
	{Route{http.MethodPost, "/api/campaigns"}, handlerOf((*api).createCampaign)},
	{Route{http.MethodGet, "/api/campaigns"}, handlerOf((*api).listCampaigns)},
	{Route{http.MethodGet, "/api/campaigns/{campaign_id}"}, handlerOf((*api).getCampaign)},
	{Route{http.MethodPost, "/api/campaigns/{campaign_id}/segment"}, handlerOf((*api).generateSegment)},
	{Route{http.MethodPost, "/api/segments/count"}, handlerOf((*api).countSegment)},

	// Content + approval slice (P4; content.go).
	{Route{http.MethodPost, "/api/campaigns/{campaign_id}/content"}, handlerOf((*api).generateContent)},
	{Route{http.MethodGet, "/api/campaigns/{campaign_id}/content-versions"}, handlerOf((*api).listContentVersions)},
	{Route{http.MethodPost, "/api/campaigns/{campaign_id}/content-versions"}, handlerOf((*api).editContent)},
	{Route{http.MethodGet, "/api/campaigns/{campaign_id}/content-versions/{version_id}"}, handlerOf((*api).getContentVersion)},
	{Route{http.MethodPost, "/api/campaigns/{campaign_id}/approve"}, handlerOf((*api).approveContent)},
	{Route{http.MethodGet, "/api/campaigns/{campaign_id}/preview"}, handlerOf((*api).previewContent)},
}

func handlerOf(m func(*api, http.ResponseWriter, *http.Request)) func(*api) http.Handler {
	return func(a *api) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m(a, w, r) })
	}
}

// Routes returns every route New registers, sorted by Template, then Method. It is a pure
// function of the code (independent of Deps: /metrics is listed even when Deps.Metrics is nil)
// and lists only explicitly registered methods.
func Routes() []Route {
	out := make([]Route, 0, len(routeTable))
	for _, e := range routeTable {
		out = append(out, e.Route)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Template != out[j].Template {
			return out[i].Template < out[j].Template
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// OpenAPI is the embedded copy of the contract (openapi.yaml + schemas/*.schema.json under
// internal/httpapi/openapi/, same relative layout as docs/contracts/). Byte-identical to
// docs/contracts/ by `make check-contract-copies`. Use fs.Sub(OpenAPI, "openapi") for the
// contract-relative root.
//
//go:embed openapi/openapi.yaml openapi/schemas/segment-rule.schema.json openapi/schemas/content.schema.json
var OpenAPI embed.FS

// metricsHandler serves Deps.Metrics; without one the route still exists (Routes lists it) and
// answers the spec'd 500 internal_error.
func (a *api) metricsHandler() http.Handler {
	if a.deps.Metrics != nil {
		return a.deps.Metrics
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal error")
	})
}

type api struct{ deps Deps }

// ---- GET /api/config ----

type appConfig struct {
	LLMProvider       llm.Provider   `json:"llm_provider"`
	MockScenarios     []llm.Scenario `json:"mock_scenarios,omitempty"`
	Limits            limits         `json:"limits"`
	TemplateVariables []string       `json:"template_variables"`
	FirstNameFallback string         `json:"first_name_fallback"`
}

type limits struct {
	SMSMaxRunes          int `json:"sms_max_runes"`
	EmailSubjectMaxRunes int `json:"email_subject_max_runes"`
	EmailBodyMaxRunes    int `json:"email_body_max_runes"`
	BriefMaxRunes        int `json:"brief_max_runes"`
	RequestBodyMaxBytes  int `json:"request_body_max_bytes"`
}

func (a *api) getConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := appConfig{
		LLMProvider: a.deps.LLMProvider,
		Limits: limits{
			SMSMaxRunes:          content.SMSMaxRunes,
			EmailSubjectMaxRunes: content.SubjectMaxRunes,
			EmailBodyMaxRunes:    content.EmailBodyMaxRunes,
			BriefMaxRunes:        campaign.BriefMaxRunes,
			RequestBodyMaxBytes:  MaxBodyBytes,
		},
		TemplateVariables: append([]string(nil), content.AllowedVariables...),
		FirstNameFallback: content.FirstNameFallback,
	}
	if a.deps.LLMProvider == llm.ProviderMock {
		cfg.MockScenarios = llm.Scenarios()
	}
	writeJSON(w, http.StatusOK, cfg)
}

// ---- routing ----

// router dispatches on the path template, then the method: unknown path => 404 not_found, known
// path with another method => 405 method_not_allowed with an Allow header. A template segment
// "{name}" matches one non-empty path segment, exposed through Request.PathValue(name); literal
// templates win over templates with parameters.
type router struct {
	routes []*route
}

type route struct {
	template string
	segments []string
	methods  map[string]http.Handler
}

func (r *router) handle(template, method string, h http.Handler) {
	for _, rt := range r.routes {
		if rt.template == template {
			rt.methods[method] = h
			return
		}
	}
	r.routes = append(r.routes, &route{
		template: template,
		segments: strings.Split(strings.TrimPrefix(template, "/"), "/"),
		methods:  map[string]http.Handler{method: h},
	})
}

// match returns the route for path and its parameters; literal templates are preferred.
func (r *router) match(path string) (*route, map[string]string) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	var best *route
	var bestParams map[string]string
	for _, rt := range r.routes {
		if len(rt.segments) != len(parts) {
			continue
		}
		params := map[string]string{}
		ok := true
		for i, seg := range rt.segments {
			if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
				if parts[i] == "" {
					ok = false
					break
				}
				params[seg[1:len(seg)-1]] = parts[i]
				continue
			}
			if seg != parts[i] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if len(params) == 0 {
			return rt, params
		}
		if best == nil {
			best, bestParams = rt, params
		}
	}
	return best, bestParams
}

func (r *router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	rt, params := r.match(req.URL.Path)
	if rt == nil {
		writeError(w, req, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if rec, ok := w.(*statusRecorder); ok {
		rec.route = rt.template
	}
	for k, v := range params {
		req.SetPathValue(k, v)
	}
	methods := rt.methods
	// Only spec'd methods are served: no implicit HEAD-on-GET (HEAD gets the 405 below).
	h, ok := methods[req.Method]
	if !ok {
		allow := make([]string, 0, len(methods))
		for m := range methods {
			allow = append(allow, m)
		}
		sort.Strings(allow)
		w.Header().Set("Allow", strings.Join(allow, ", "))
		writeError(w, req, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	h.ServeHTTP(w, req)
}

// ---- middleware ----

func (a *api) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		start := a.deps.Clock.Now()
		w := &statusRecorder{ResponseWriter: rw, route: "unmatched"}
		defer func() {
			metrics.ObserveHTTPRequest(r.Method, w.route, w.statusCode(), a.deps.Clock.Now().Sub(start), w.streaming)
		}()
		id := requestID(r.Header.Get("X-Request-ID"))
		w.Header().Set("X-Request-ID", id)
		r.Header.Set("X-Request-ID", id)
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		}
		defer func() {
			if rec := recover(); rec != nil {
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				a.deps.Logger.Error("http handler panic", slog.String("request_id", id),
					slog.String("method", r.Method), slog.String("path", r.URL.Path))
				if w.streaming {
					// An open event stream cannot take a JSON error body or a second status; the
					// SSE handler reports its own failures as a terminal event.
					return
				}
				writeError(w, r, http.StatusInternalServerError, "internal_error", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code and the matched route template for the HTTP metrics.
type statusRecorder struct {
	http.ResponseWriter
	status    int
	route     string
	streaming bool // set by SSE handlers: counted, not timed
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Flush and Unwrap keep streaming (http.Flusher, http.ResponseController) working through the wrapper.
func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusRecorder) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// requestID echoes a client-supplied id when it is short and printable, otherwise makes one.
func requestID(in string) string {
	if in != "" && len(in) <= 128 {
		ok := true
		for _, c := range in {
			if c < 0x21 || c > 0x7e {
				ok = false
				break
			}
		}
		if ok {
			return in
		}
	}
	return uuid.NewString()
}

// ---- responses ----

type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code      string                  `json:"code"`
	Message   string                  `json:"message"`
	Fields    []validation.FieldError `json:"fields,omitempty"`
	RequestID string                  `json:"request_id"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorPayload{Code: code, Message: message, RequestID: r.Header.Get("X-Request-ID")}})
}

// writeFieldError writes an error that carries field errors (validation_failed, llm_invalid_output).
// The spec requires a non-empty `fields` array for these codes, so an error that arrives without
// field errors gets one whole-document entry instead of an empty or missing array.
func writeFieldError(w http.ResponseWriter, r *http.Request, status int, code, message string, fields []validation.FieldError) {
	if len(fields) == 0 {
		fields = []validation.FieldError{{Path: "", Code: validation.CodeSchemaViolation, Message: message}}
	}
	writeJSON(w, status, errorBody{Error: errorPayload{
		Code: code, Message: message, Fields: fields, RequestID: r.Header.Get("X-Request-ID"),
	}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // the client may have gone; nothing left to do
}
