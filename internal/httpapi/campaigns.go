package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"sort"
	"strconv"

	"github.com/google/uuid"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/composer"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// mockScenarioHeader selects the mock LLM scenario (openapi MockScenario parameter).
const mockScenarioHeader = "X-Mock-LLM-Scenario"

// ---- POST /api/campaigns ----

func (a *api) createCampaign(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r, true)
	if !ok {
		return
	}
	obj, ok := decodeObject(w, r, body)
	if !ok {
		return
	}
	var fields []validation.FieldError
	for _, k := range sortedKeys(obj) {
		if k != "brief" {
			fields = append(fields, schemaViolation(k, "unknown property"))
		}
	}
	var brief string
	switch raw, present := obj["brief"]; {
	case !present:
		fields = append(fields, schemaViolation("brief", "brief is missing"))
	case json.Unmarshal(raw, &brief) != nil || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)):
		fields = append(fields, schemaViolation("brief", "brief must be a string"))
	}
	if len(fields) > 0 {
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
		return
	}
	// The brief rules (NFC, trim, required, <= 2000 runes) live in campaign.NormalizeBrief inside
	// the service; its *validation.Error is mapped to 422 below.
	c, err := a.deps.Service.Create(r.Context(), brief)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/campaigns/"+c.ID)
	writeJSON(w, http.StatusCreated, c)
}

// ---- GET /api/campaigns ----

func (a *api) listCampaigns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var fields []validation.FieldError
	limit, ok := intQuery(q.Get("limit"), campaign.DefaultPageLimit, 1, campaign.MaxPageLimit)
	if !ok {
		fields = append(fields, invalidQuery("limit", "limit must be an integer between 1 and "+strconv.Itoa(campaign.MaxPageLimit)))
	}
	offset, ok := intQuery(q.Get("offset"), 0, 0, -1)
	if !ok {
		fields = append(fields, invalidQuery("offset", "offset must be a non-negative integer"))
	}
	if len(fields) > 0 {
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
		return
	}
	page, err := a.deps.Service.List(r.Context(), limit, offset)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// ---- GET /api/campaigns/{campaign_id} ----

func (a *api) getCampaign(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	c, err := a.deps.Service.Get(r.Context(), id)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// ---- POST /api/campaigns/{campaign_id}/segment ----

func (a *api) generateSegment(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	ctx, ok := a.scenarioContext(w, r)
	if !ok {
		return
	}
	if !readEmptyObject(w, r) {
		return
	}
	res, err := a.deps.Service.GenerateSegment(ctx, id)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- POST /api/segments/count ----

func (a *api) countSegment(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r, true)
	if !ok {
		return
	}
	res, err := a.deps.Service.CountRule(r.Context(), body)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- helpers ----

// campaignID returns the path's campaign id; a malformed UUID is "not found" (openapi).
func campaignID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("campaign_id")
	if _, err := uuid.Parse(id); err != nil || len(id) != 36 {
		writeError(w, r, http.StatusNotFound, "campaign_not_found", "campaign not found")
		return "", false
	}
	return id, true
}

// scenarioContext applies X-Mock-LLM-Scenario when the effective provider is the mock; with the
// real client the header is ignored. An unknown name is a 422 and the LLM is not called.
func (a *api) scenarioContext(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	ctx := r.Context()
	v := r.Header.Get(mockScenarioHeader)
	if a.deps.LLMProvider != llm.ProviderMock || v == "" {
		return ctx, true
	}
	sc, err := llm.ParseScenario(v)
	if err != nil {
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed",
			[]validation.FieldError{{Path: "header." + mockScenarioHeader, Code: validation.CodeInvalidValue,
				Message: "unknown mock LLM scenario"}})
		return nil, false
	}
	return llm.WithScenario(ctx, sc), true
}

// readBody reads the request body. Larger than MaxBodyBytes => 413; a non-empty body that is not
// JSON (by Content-Type or content) => 400; an empty body is allowed only when !required.
func readBody(w http.ResponseWriter, r *http.Request, required bool) ([]byte, bool) {
	var body []byte
	if r.Body != nil {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large",
					"request body exceeds "+strconv.Itoa(MaxBodyBytes)+" bytes")
				return nil, false
			}
			writeError(w, r, http.StatusBadRequest, "malformed_request", "request body could not be read")
			return nil, false
		}
		body = b
	}
	if len(bytes.TrimSpace(body)) == 0 {
		if required {
			writeError(w, r, http.StatusBadRequest, "malformed_request", "request body must be a JSON document")
			return nil, false
		}
		return nil, true
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeError(w, r, http.StatusBadRequest, "malformed_request", "Content-Type must be application/json")
		return nil, false
	}
	if !json.Valid(body) {
		writeError(w, r, http.StatusBadRequest, "malformed_request", "request body is not valid JSON")
		return nil, false
	}
	return body, true
}

// readEmptyObject accepts "none or {}" bodies; any property is a schema violation.
func readEmptyObject(w http.ResponseWriter, r *http.Request) bool {
	body, ok := readBody(w, r, false)
	if !ok {
		return false
	}
	if body == nil {
		return true
	}
	obj, ok := decodeObject(w, r, body)
	if !ok {
		return false
	}
	if len(obj) == 0 {
		return true
	}
	var fields []validation.FieldError
	for _, k := range sortedKeys(obj) {
		fields = append(fields, schemaViolation(k, "unknown property"))
	}
	writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
	return false
}

// decodeObject requires a JSON object (body already known to be valid JSON).
func decodeObject(w http.ResponseWriter, r *http.Request, body []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) || json.Unmarshal(body, &obj) != nil {
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed",
			[]validation.FieldError{schemaViolation("", "request body must be a JSON object")})
		return nil, false
	}
	return obj, true
}

// intQuery parses an optional integer query parameter within [lo, hi] (hi < 0: no upper bound).
func intQuery(v string, def, lo, hi int) (int, bool) {
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || (hi >= 0 && n > hi) {
		return 0, false
	}
	return n, true
}

func invalidQuery(name, msg string) validation.FieldError {
	return validation.FieldError{Path: "query." + name, Code: validation.CodeInvalidValue, Message: msg}
}

func schemaViolation(path, msg string) validation.FieldError {
	return validation.FieldError{Path: path, Code: validation.CodeSchemaViolation, Message: msg}
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writeServiceError maps a service error to the contract's HTTP status and error code
// (contracts README §5.3). Messages are fixed strings: no database text, raw model output or
// secret ever reaches the response.
func (a *api) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *validation.Error
	switch {
	case errors.Is(err, composer.ErrInvalidOutput):
		var fields []validation.FieldError
		if errors.As(err, &ve) {
			fields = ve.Fields
		}
		if len(fields) == 0 {
			fields = []validation.FieldError{{Path: "", Code: validation.CodeSchemaViolation, Message: "invalid model output"}}
		}
		writeFieldError(w, r, http.StatusBadGateway, "llm_invalid_output",
			"the language model did not return a valid result after one repair attempt", fields)
	case errors.As(err, &ve):
		writeFieldError(w, r, http.StatusUnprocessableEntity, "validation_failed", "validation failed", ve.Fields)
	case errors.Is(err, llm.ErrUnavailable):
		writeError(w, r, http.StatusBadGateway, "llm_unavailable", "the language model is unavailable")
	case errors.Is(err, campaign.ErrCampaignNotFound):
		writeError(w, r, http.StatusNotFound, "campaign_not_found", "campaign not found")
	case errors.Is(err, campaign.ErrContentVersionNotFound):
		writeError(w, r, http.StatusNotFound, "content_version_not_found", "content version not found")
	case errors.Is(err, campaign.ErrCustomerNotFound):
		writeError(w, r, http.StatusNotFound, "customer_not_found", "customer not found")
	case errors.Is(err, campaign.ErrGenerationInProgress):
		writeError(w, r, http.StatusConflict, "generation_in_progress", "a generation is already running for this campaign")
	case errors.Is(err, campaign.ErrInvalidState):
		writeError(w, r, http.StatusConflict, "invalid_state", "the campaign state does not allow this operation")
	case errors.Is(err, campaign.ErrContentNotValidated):
		writeError(w, r, http.StatusConflict, "content_not_validated", "the content version is not validated")
	case errors.Is(err, campaign.ErrStaleContentVersion):
		writeError(w, r, http.StatusConflict, "stale_content_version", "the content version is not the current one")
	case errors.Is(err, context.Canceled) && r.Context().Err() != nil:
		// The client went away; nobody reads the response.
		a.deps.Logger.Info("request cancelled", slog.String("request_id", r.Header.Get("X-Request-ID")))
	default:
		a.deps.Logger.Error("request failed", slog.String("request_id", r.Header.Get("X-Request-ID")),
			slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.String("error", err.Error()))
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal error")
	}
}
