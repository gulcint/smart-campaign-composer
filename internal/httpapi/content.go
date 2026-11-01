package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"sync"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/composer"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/sse"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// expectedVersionHeader is editContent's optimistic-concurrency precondition (openapi ExpectedVersion).
const expectedVersionHeader = "X-Expected-Version"

// expectedVersionPattern is the header's schema pattern: "none" or a lower-case UUID.
var expectedVersionPattern = regexp.MustCompile(`^(none|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

// ---- POST /api/campaigns/{campaign_id}/content (SSE) ----

func (a *api) generateContent(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	ctx, ok := a.scenarioContext(w, r)
	if !ok {
		return
	}
	ctx, cancel := a.streamContext(ctx)
	defer cancel()
	if !readEmptyObject(w, r) {
		return
	}
	run, err := a.deps.Service.StartContentGeneration(ctx, id)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	sw, err := sse.NewWriter(w)
	if err != nil {
		// The run's generation lock ends with the request context.
		a.writeServiceError(w, r, err)
		return
	}
	if rec, ok := w.(*statusRecorder); ok {
		rec.streaming = true
	}
	w.WriteHeader(http.StatusOK)
	sw.Flush()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	defer func() {
		close(done)
		wg.Wait()
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-a.deps.Clock.After(a.deps.HeartbeatInterval):
				if sw.Comment("keepalive") != nil {
					return
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	attempt, terminal := 1, false
	// internalError ends the stream with the terminal internal_error event: after HTTP 200 a
	// failure is reported only through events (sse-events.md §0), never as a JSON error body.
	internalError := func(cause string) {
		a.deps.Logger.Error("content stream failed", slog.String("request_id", r.Header.Get("X-Request-ID")),
			slog.String("campaign_id", id), slog.String("error", cause))
		if terminal || ctx.Err() != nil {
			return
		}
		_ = sw.Event("invalid", composer.InvalidEvent{Attempt: attempt, Final: true, Errors: []validation.FieldError{{
			Path: "", Code: validation.CodeInternalError, Message: "internal error",
		}}})
	}
	defer func() {
		rec := recover()
		if rec == nil {
			return
		}
		if e, ok := rec.(error); ok && errors.Is(e, http.ErrAbortHandler) {
			panic(rec)
		}
		internalError("panic during the content stream")
	}()
	err = run.Stream(ctx, func(ev composer.Event) error {
		switch e := ev.(type) {
		case composer.DeltaEvent:
			attempt = e.Attempt
		case composer.InvalidEvent:
			attempt, terminal = e.Attempt, e.Final
		case composer.ValidatedEvent:
			terminal = true
		}
		return sw.Event(ev.Name(), ev)
	})
	if err != nil && !terminal && ctx.Err() == nil {
		internalError(err.Error())
	}
}

// ---- GET /api/campaigns/{campaign_id}/content-versions ----

type versionList struct {
	Items []campaign.ContentVersion `json:"items"`
}

func (a *api) listContentVersions(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	vs, err := a.deps.Service.ListContentVersions(r.Context(), id)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	if vs == nil {
		vs = []campaign.ContentVersion{}
	}
	writeJSON(w, http.StatusOK, versionList{Items: vs})
}

// ---- POST /api/campaigns/{campaign_id}/content-versions ----

func (a *api) editContent(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	expected, present := r.Header[http.CanonicalHeaderKey(expectedVersionHeader)]
	switch {
	case !present || len(expected) == 0 || expected[0] == "":
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed",
			[]validation.FieldError{{Path: "header." + expectedVersionHeader, Code: validation.CodeSchemaViolation,
				Message: expectedVersionHeader + " header is required"}})
		return
	case len(expected) > 1 || !expectedVersionPattern.MatchString(expected[0]):
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed",
			[]validation.FieldError{{Path: "header." + expectedVersionHeader, Code: validation.CodeInvalidValue,
				Message: expectedVersionHeader + " must be a content version id or none"}})
		return
	}
	body, ok := readBody(w, r, true)
	if !ok {
		return
	}
	v, err := a.deps.Service.EditContent(r.Context(), id, expected[0], body)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// ---- GET /api/campaigns/{campaign_id}/content-versions/{version_id} ----

func (a *api) getContentVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	vid, ok := canonicalUUID(r.PathValue("version_id"))
	if !ok {
		// README §5.4: the campaign is checked first, a malformed version id is then not found.
		if _, err := a.deps.Service.Get(r.Context(), id); err != nil {
			a.writeServiceError(w, r, err)
			return
		}
		writeError(w, r, http.StatusNotFound, "content_version_not_found", "content version not found")
		return
	}
	v, err := a.deps.Service.GetContentVersion(r.Context(), id, vid)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// ---- POST /api/campaigns/{campaign_id}/approve ----

func (a *api) approveContent(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	vid, ok := readVersionRef(w, r)
	if !ok {
		return
	}
	c, err := a.deps.Service.Approve(r.Context(), id, vid)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// readVersionRef reads the VersionRef body {"content_version_id": "<uuid>"}.
func readVersionRef(w http.ResponseWriter, r *http.Request) (string, bool) {
	body, ok := readBody(w, r, true)
	if !ok {
		return "", false
	}
	obj, ok := decodeObject(w, r, body)
	if !ok {
		return "", false
	}
	vid, fields := versionRefFields(obj)
	if len(fields) > 0 {
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
		return "", false
	}
	return vid, true
}

// versionRefFields checks a decoded body that carries content_version_id and the optional
// properties in more: unknown properties (keys in byte order), then content_version_id. It returns
// the canonical version id and the field errors in that order.
func versionRefFields(obj map[string]json.RawMessage, more ...string) (string, []validation.FieldError) {
	var fields []validation.FieldError
	for _, k := range sortedKeys(obj) {
		if k != "content_version_id" && !slices.Contains(more, k) {
			fields = append(fields, schemaViolation(k, "unknown property"))
		}
	}
	var vid string
	switch raw, present := obj["content_version_id"]; {
	case !present:
		fields = append(fields, schemaViolation("content_version_id", "content_version_id is missing"))
	case !bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) || json.Unmarshal(raw, &vid) != nil:
		fields = append(fields, schemaViolation("content_version_id", "content_version_id must be a string"))
	default:
		var ok bool
		if vid, ok = canonicalUUID(vid); !ok {
			fields = append(fields, validation.FieldError{Path: "content_version_id", Code: validation.CodeInvalidValue,
				Message: "content_version_id must be a UUID"})
		}
	}
	return vid, fields
}

// ---- GET /api/campaigns/{campaign_id}/preview ----

func (a *api) previewContent(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	var fields []validation.FieldError
	q := campaign.PreviewQuery{CustomerID: query.Get("customer_id"), ContentVersionID: query.Get("content_version_id")}
	if q.CustomerID != "" {
		if q.CustomerID, ok = canonicalUUID(q.CustomerID); !ok {
			fields = append(fields, invalidQuery("customer_id", "customer_id must be a UUID"))
		}
	}
	if q.ContentVersionID != "" {
		if q.ContentVersionID, ok = canonicalUUID(q.ContentVersionID); !ok {
			fields = append(fields, invalidQuery("content_version_id", "content_version_id must be a UUID"))
		}
	}
	n, ok := intQuery(query.Get("sample_index"), 0, 0, -1)
	if !ok {
		fields = append(fields, invalidQuery("sample_index", "sample_index must be a non-negative integer"))
	}
	// P8 (D63): optional language tr | en; absent = the sample customer's own language.
	if q.Language = query.Get("language"); query.Has("language") && q.Language != content.LangTR && q.Language != content.LangEN {
		fields = append(fields, invalidQuery("language", "language must be tr or en"))
	}
	if len(fields) > 0 {
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
		return
	}
	q.SampleIndex = n
	p, err := a.deps.Service.Preview(r.Context(), id, q)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// ---- content_edit_conflict ----

type conflictBody struct {
	Error conflictPayload `json:"error"`
}

type conflictPayload struct {
	Code           string                      `json:"code"`
	Message        string                      `json:"message"`
	CurrentVersion *campaign.EditConflictError `json:"current_version"`
	RequestID      string                      `json:"request_id"`
}

// writeEditConflict writes 409 content_edit_conflict with error.current_version (README §5.3).
func writeEditConflict(w http.ResponseWriter, r *http.Request, err error) bool {
	var ce *campaign.EditConflictError
	if !errors.As(err, &ce) {
		return false
	}
	msg := "the content changed since the editor loaded it"
	if ce.CurrentVersionNumber != nil {
		msg += "; the latest version is number " + strconv.Itoa(*ce.CurrentVersionNumber)
	}
	writeJSON(w, http.StatusConflict, conflictBody{Error: conflictPayload{
		Code: "content_edit_conflict", Message: msg, CurrentVersion: ce, RequestID: r.Header.Get("X-Request-ID"),
	}})
	return true
}
