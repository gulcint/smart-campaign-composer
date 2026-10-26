// Package httpapi is the HTTP router of cmd/api (docs/contracts/openapi.yaml).
//
// P2 serves the system endpoints (/healthz, /readyz, /metrics, /api/config) plus
// the cross-cutting behaviour every route shares: X-Request-ID, the 64 KiB body
// limit, JSON 404/405 errors and panic recovery. The campaign routes are added by
// the feature slices (P3–P5) on top of the same Deps.
package httpapi

import (
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

	r := &router{routes: map[string]map[string]http.Handler{}}
	r.handle("/healthz", http.MethodGet, health.Healthz())
	r.handle("/readyz", http.MethodGet, health.Readyz(d.DB))
	if d.Metrics != nil {
		r.handle("/metrics", http.MethodGet, d.Metrics)
	}
	r.handle("/api/config", http.MethodGet, http.HandlerFunc(a.getConfig))

	return a.middleware(r)
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

// router dispatches on exact path, then method: unknown path => 404 not_found, known path with
// another method => 405 method_not_allowed with an Allow header.
type router struct {
	routes map[string]map[string]http.Handler
}

func (r *router) handle(path, method string, h http.Handler) {
	if r.routes[path] == nil {
		r.routes[path] = map[string]http.Handler{}
	}
	r.routes[path][method] = h
}

func (r *router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	methods, ok := r.routes[req.URL.Path]
	if !ok {
		writeError(w, req, http.StatusNotFound, "not_found", "route not found")
		return
	}
	h, ok := methods[req.Method]
	if !ok && req.Method == http.MethodHead {
		h, ok = methods[http.MethodGet]
	}
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				writeError(w, r, http.StatusInternalServerError, "internal_error", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
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
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorPayload{Code: code, Message: message, RequestID: r.Header.Get("X-Request-ID")}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // the client may have gone; nothing left to do
}
