// Package apispec is the test-side half of the API ↔ doc drift guard (docs/contracts/README.md §11,
// docs/contracts/go-interfaces.md §httpapi "Rota tablosu ve sözleşme kapsamı").
//
// It loads docs/contracts/openapi.yaml FROM DISK (OPENAPI_SPEC_PATH, or <module root>/docs/contracts/
// openapi.yaml) with libopenapi DocumentConfiguration{BasePath: <spec dir>, AllowFileReferences: true},
// so the relative schemas/*.schema.json refs resolve. The embedded copy (httpapi.OpenAPI) is never
// loaded through an fs.FS: the contract records that libopenapi's LocalFS mode cannot resolve
// "#/$defs/..." refs inside the external schema files.
//
// Transport is an http.RoundTripper for the E2E client. With OPENAPI_VALIDATE=strict it checks every
// request/response pair sent to the API base URL:
//
//   - the response status must be listed for the matched operation, and its body must validate
//     against the spec (closed schemas, pinned error codes); text/event-stream bodies of operations
//     carrying x-sse-events are not read or validated;
//   - the request (parameters and body) is validated only when the response is 2xx: tests send
//     invalid requests on purpose and those must get a 4xx; a 2xx for an invalid request is drift;
//   - responses that belong to no operation are checked against components.responses.RouteNotFound
//     (no path template matches => 404) and components.responses.MethodNotAllowed (path known,
//     method not defined => 405 with an Allow header);
//   - the operationId of every matched request is recorded for the coverage check (AfterSuite).
//
// A violation makes RoundTrip return an error matching ErrViolation, so the calling test fails at
// its client.Do with a readable list of problems.
package apispec

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	validator "github.com/pb33f/libopenapi-validator"
	liberrors "github.com/pb33f/libopenapi-validator/errors"
	"github.com/pb33f/libopenapi-validator/schema_validation"

	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
)

// Environment variables of the strict E2E validation (docs/contracts/README.md §9 #15, §11).
const (
	EnvValidate = "OPENAPI_VALIDATE"
	EnvSpecPath = "OPENAPI_SPEC_PATH"
	ModeStrict  = "strict"
)

// ErrViolation is matched (errors.Is) by every error Transport returns for a request or response
// that does not follow openapi.yaml.
var ErrViolation = errors.New("apispec: request/response does not follow docs/contracts/openapi.yaml")

// Router-level response components (openapi.yaml components.responses).
const (
	routeNotFound    = "RouteNotFound"
	methodNotAllowed = "MethodNotAllowed"
)

// Operation is one method + path template of openapi.yaml.
type Operation struct {
	ID       string // operationId
	Method   string // upper case, e.g. "POST"
	Template string // key under paths:, e.g. "/api/campaigns/{campaign_id}/segment"
	SSE      bool   // the operation carries x-sse-events
}

// Spec is a loaded openapi.yaml.
type Spec struct {
	path      string
	model     *v3.Document
	ops       []Operation
	byKey     map[string]*v3.Operation // "METHOD template"
	byPath    map[string][]string      // template -> methods defined (sorted)
	templates []string

	mu        sync.Mutex // the validator is used from parallel tests; serialize it
	validator validator.Validator
	schemas   schema_validation.SchemaValidator
}

// ModuleRoot walks up from the working directory to the directory that contains go.mod.
func ModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("apispec: go.mod not found above the working directory")
		}
		dir = parent
	}
}

// SpecPath returns OPENAPI_SPEC_PATH, or <module root>/docs/contracts/openapi.yaml when it is empty.
func SpecPath() (string, error) {
	if p := os.Getenv(EnvSpecPath); p != "" {
		return p, nil
	}
	root, err := ModuleRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "docs", "contracts", "openapi.yaml"), nil
}

// Load reads and resolves the spec at path (relative $refs resolve against its directory).
func Load(path string) (*Spec, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("apispec: read spec: %w", err)
	}
	doc, err := libopenapi.NewDocumentWithConfiguration(raw, &datamodel.DocumentConfiguration{
		BasePath:            filepath.Dir(abs),
		AllowFileReferences: true,
	})
	if err != nil {
		return nil, fmt.Errorf("apispec: parse %s: %w", abs, err)
	}
	built, buildErrs := doc.BuildV3Model()
	if built == nil || hasErrors(buildErrs) {
		return nil, fmt.Errorf("apispec: build OpenAPI 3 model of %s: %w", abs, buildErrs)
	}
	s := &Spec{
		path:      abs,
		model:     &built.Model,
		byKey:     map[string]*v3.Operation{},
		byPath:    map[string][]string{},
		validator: validator.NewValidatorFromV3Model(&built.Model),
		schemas:   schema_validation.NewSchemaValidator(),
	}
	if s.model.Paths == nil || s.model.Paths.PathItems == nil {
		return nil, fmt.Errorf("apispec: %s has no paths", abs)
	}
	for tpl, item := range s.model.Paths.PathItems.FromOldest() {
		s.templates = append(s.templates, tpl)
		for _, mo := range methodOperations(item) {
			op := Operation{Method: mo.method, Template: tpl, ID: mo.op.OperationId, SSE: hasExtension(mo.op, "x-sse-events")}
			s.ops = append(s.ops, op)
			s.byKey[mo.method+" "+tpl] = mo.op
			s.byPath[tpl] = append(s.byPath[tpl], mo.method)
		}
		slices.Sort(s.byPath[tpl])
	}
	slices.SortFunc(s.ops, func(a, b Operation) int {
		if c := strings.Compare(a.Template, b.Template); c != 0 {
			return c
		}
		return strings.Compare(a.Method, b.Method)
	})
	return s, nil
}

// hasErrors accepts both an error and an []error (the BuildV3Model return type changed across
// libopenapi releases).
func hasErrors(x any) bool {
	switch e := x.(type) {
	case nil:
		return false
	case []error:
		return len(e) > 0
	case error:
		return e != nil
	}
	return false
}

type methodOp struct {
	method string
	op     *v3.Operation
}

func methodOperations(p *v3.PathItem) []methodOp {
	all := []methodOp{
		{http.MethodGet, p.Get}, {http.MethodPut, p.Put}, {http.MethodPost, p.Post}, {http.MethodDelete, p.Delete},
		{http.MethodOptions, p.Options}, {http.MethodHead, p.Head}, {http.MethodPatch, p.Patch}, {http.MethodTrace, p.Trace},
	}
	out := all[:0]
	for _, mo := range all {
		if mo.op != nil {
			out = append(out, mo)
		}
	}
	return out
}

func hasExtension(op *v3.Operation, name string) bool {
	if op.Extensions == nil {
		return false
	}
	_, ok := op.Extensions.Get(name)
	return ok
}

// Path is the file the spec was loaded from.
func (s *Spec) Path() string { return s.path }

// Operations returns every operation, sorted by Template, then Method.
func (s *Spec) Operations() []Operation { return slices.Clone(s.ops) }

// Match finds the operation for a request path. pathKnown reports whether some path template
// matches; op is nil when the template does not define method. Templates with more literal
// segments win (e.g. "/a/b" over "/a/{x}").
func (s *Spec) Match(method, path string) (op *Operation, template string, pathKnown bool) {
	segs := strings.Split(path, "/")
	best, bestScore := "", -1
	for _, tpl := range s.templates {
		t := strings.Split(tpl, "/")
		if len(t) != len(segs) {
			continue
		}
		score, ok := 0, true
		for i := range t {
			isParam := strings.HasPrefix(t[i], "{") && strings.HasSuffix(t[i], "}")
			switch {
			case isParam && segs[i] != "":
			case t[i] == segs[i]:
				score++
			default:
				ok = false
			}
			if !ok {
				break
			}
		}
		if ok && score > bestScore {
			best, bestScore = tpl, score
		}
	}
	if bestScore < 0 {
		return nil, "", false
	}
	for i := range s.ops {
		if s.ops[i].Template == best && s.ops[i].Method == method {
			o := s.ops[i]
			return &o, best, true
		}
	}
	return nil, best, true
}

// Uncovered returns the operationIds (sorted) that are implemented — every spec operation not in
// notImplemented — but have no entry in calls.
func (s *Spec) Uncovered(calls map[string]int, notImplemented map[string]string) []string {
	var out []string
	for _, op := range s.ops {
		if _, skip := notImplemented[op.ID]; skip {
			continue
		}
		if calls[op.ID] == 0 {
			out = append(out, op.ID)
		}
	}
	slices.Sort(out)
	return out
}

// UnknownIDs returns the keys of ids that are not operationIds of the spec (sorted).
func (s *Spec) UnknownIDs(ids map[string]string) []string {
	known := map[string]bool{}
	for _, op := range s.ops {
		known[op.ID] = true
	}
	var out []string
	for id := range ids {
		if !known[id] {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// check returns the problems of one exchange. req carries the URL/method/headers; reqBody and
// respBody are the full bodies (respBody is nil for an unread SSE stream).
func (s *Spec) check(req *http.Request, reqBody []byte, resp *http.Response, respBody []byte, op *Operation, tpl string, pathKnown bool) []string {
	mediaType := responseMediaType(resp)
	switch {
	case !pathKnown:
		return s.checkRouterLevel(routeNotFound, http.StatusNotFound, resp, respBody, mediaType, nil)
	case op == nil:
		return s.checkRouterLevel(methodNotAllowed, http.StatusMethodNotAllowed, resp, respBody, mediaType, s.byPath[tpl])
	}

	var problems []string
	o := s.byKey[op.Method+" "+op.Template]
	r := listedResponse(o, resp.StatusCode)
	if r == nil {
		problems = append(problems, fmt.Sprintf("status %d is not listed for operationId %s (listed: %s)", resp.StatusCode, op.ID, strings.Join(listedCodes(o), ", ")))
	} else if !hasMediaType(r, mediaType) {
		problems = append(problems, fmt.Sprintf("response Content-Type %q is not listed for status %d of operationId %s", resp.Header.Get("Content-Type"), resp.StatusCode, op.ID))
	}

	sseBody := op.SSE && mediaType == "text/event-stream"
	s.mu.Lock()
	defer s.mu.Unlock()
	if !sseBody {
		vreq := cloneRequest(req, nil)
		vresp := cloneResponse(resp, respBody) // in-memory copy; the network body was closed after reading
		ok, errs := s.validator.ValidateHttpResponse(vreq, vresp)
		_ = vresp.Body.Close()
		if !ok || len(errs) > 0 {
			problems = append(problems, describe("response", errs)...)
		}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		vreq := cloneRequest(req, reqBody)
		if ok, errs := s.validator.ValidateHttpRequest(vreq); !ok || len(errs) > 0 {
			problems = append(problems, describe(fmt.Sprintf("request answered with %d", resp.StatusCode), errs)...)
		}
	}
	return problems
}

func (s *Spec) checkRouterLevel(component string, wantStatus int, resp *http.Response, body []byte, mediaType string, methods []string) []string {
	var problems []string
	if resp.StatusCode != wantStatus {
		problems = append(problems, fmt.Sprintf("router-level response must be %d (components.responses.%s), got %d", wantStatus, component, resp.StatusCode))
	}
	if s.model.Components == nil || s.model.Components.Responses == nil {
		return append(problems, "spec has no components.responses")
	}
	r, ok := s.model.Components.Responses.Get(component)
	if !ok || r == nil {
		return append(problems, "spec has no components.responses."+component)
	}
	if methods != nil {
		allow := resp.Header.Get("Allow")
		if allow == "" {
			problems = append(problems, "405 response has no Allow header (components.responses.MethodNotAllowed requires it)")
		} else {
			got := map[string]bool{}
			for _, m := range strings.Split(allow, ",") {
				got[strings.ToUpper(strings.TrimSpace(m))] = true
			}
			for _, m := range methods {
				if !got[m] {
					problems = append(problems, fmt.Sprintf("Allow %q does not list %s, which the path defines", allow, m))
				}
			}
		}
	}
	if r.Content == nil {
		return append(problems, "components.responses."+component+" has no content")
	}
	mt, ok := r.Content.Get(mediaType)
	if !ok || mt == nil || mt.Schema == nil {
		return append(problems, fmt.Sprintf("Content-Type %q is not listed in components.responses.%s", resp.Header.Get("Content-Type"), component))
	}
	schema := mt.Schema.Schema()
	if schema == nil {
		return append(problems, "components.responses."+component+" schema does not resolve")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok, errs := s.validateSchema(schema, body); !ok || len(errs) > 0 {
		problems = append(problems, describe("router-level body ("+component+")", errs)...)
	}
	return problems
}

func (s *Spec) validateSchema(schema *base.Schema, body []byte) (bool, []*liberrors.ValidationError) {
	return s.schemas.ValidateSchemaBytes(schema, body)
}

func listedResponse(o *v3.Operation, status int) *v3.Response {
	if o.Responses == nil {
		return nil
	}
	if o.Responses.Codes != nil {
		if r, ok := o.Responses.Codes.Get(strconv.Itoa(status)); ok && r != nil {
			return r
		}
	}
	return o.Responses.Default
}

func listedCodes(o *v3.Operation) []string {
	var out []string
	if o.Responses != nil && o.Responses.Codes != nil {
		for code := range o.Responses.Codes.FromOldest() {
			out = append(out, code)
		}
	}
	slices.Sort(out)
	return out
}

func hasMediaType(r *v3.Response, mediaType string) bool {
	if r.Content == nil {
		return mediaType == ""
	}
	_, ok := r.Content.Get(mediaType)
	return ok
}

func responseMediaType(resp *http.Response) string {
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ct
	}
	return mt
}

func describe(prefix string, errs []*liberrors.ValidationError) []string {
	if len(errs) == 0 {
		return []string{prefix + ": rejected by the validator without details"}
	}
	var out []string
	for _, e := range errs {
		if e == nil {
			continue
		}
		var b strings.Builder
		b.WriteString(prefix)
		b.WriteString(": ")
		b.WriteString(e.Message)
		if e.Reason != "" && e.Reason != e.Message {
			b.WriteString(" — ")
			b.WriteString(e.Reason)
		}
		for _, f := range e.SchemaValidationErrors {
			if f != nil && f.Reason != "" {
				b.WriteString("\n      · ")
				b.WriteString(f.Reason)
			}
		}
		out = append(out, b.String())
	}
	return out
}

func cloneRequest(req *http.Request, body []byte) *http.Request {
	c := req.Clone(req.Context())
	if len(body) == 0 {
		c.Body, c.GetBody, c.ContentLength = nil, nil, 0
		return c
	}
	c.Body = io.NopCloser(bytes.NewReader(body))
	c.ContentLength = int64(len(body))
	c.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return c
}

func cloneResponse(resp *http.Response, body []byte) *http.Response {
	c := *resp
	c.Header = resp.Header.Clone()
	c.Body = io.NopCloser(bytes.NewReader(body))
	c.ContentLength = int64(len(body))
	return &c
}

// Violation describes every problem of one exchange. It matches ErrViolation.
type Violation struct {
	Method, URL string
	Status      int
	OperationID string // "" for router-level responses
	Problems    []string
}

func (v *Violation) Error() string {
	op := v.OperationID
	if op == "" {
		op = "router-level, no operation"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s=%s: %s %s -> %d (%s) violates docs/contracts/openapi.yaml:", EnvValidate, ModeStrict, v.Method, v.URL, v.Status, op)
	for _, p := range v.Problems {
		b.WriteString("\n    - ")
		b.WriteString(p)
	}
	return b.String()
}

func (v *Violation) Is(target error) bool { return target == ErrViolation }

// Transport validates requests to the API against the spec (see the package documentation).
// Use a *Transport; the zero value with Strict=false passes everything through.
type Transport struct {
	// Next sends the request; nil => http.DefaultTransport at request time (so testenv's network
	// guard, installed in TestMain after package initialization, stays in the chain).
	Next http.RoundTripper
	// Strict turns validation and coverage recording on.
	Strict bool
	// Scope is the API base URL; only requests to its scheme+host are checked. If empty, the base
	// URL is read from the environment variable ScopeEnv at request time; if both are empty every
	// request is checked.
	Scope    string
	ScopeEnv string
	// SpecPath is the spec file; empty => SpecPath().
	SpecPath string

	once    sync.Once
	spec    *Spec
	specErr error

	mu         sync.Mutex
	calls      map[string]int
	violations []string
}

// FromEnv returns the E2E transport: strict iff OPENAPI_VALIDATE=strict, scoped to the base URL in
// the environment variable scopeEnv (e.g. "E2E_API_URL"), spec from OPENAPI_SPEC_PATH.
func FromEnv(scopeEnv string) *Transport {
	return &Transport{Strict: os.Getenv(EnvValidate) == ModeStrict, ScopeEnv: scopeEnv}
}

// Spec loads the spec once.
func (t *Transport) Spec() (*Spec, error) {
	t.once.Do(func() {
		p := t.SpecPath
		if p == "" {
			p, t.specErr = SpecPath()
			if t.specErr != nil {
				return
			}
		}
		t.spec, t.specErr = Load(p)
	})
	return t.spec, t.specErr
}

func (t *Transport) next() http.RoundTripper {
	if t.Next != nil {
		return t.Next
	}
	return http.DefaultTransport
}

func (t *Transport) inScope(u *url.URL) bool {
	base := t.Scope
	if base == "" && t.ScopeEnv != "" {
		base = os.Getenv(t.ScopeEnv)
		if base == "" {
			return false
		}
	}
	if base == "" {
		return true
	}
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	return strings.EqualFold(b.Scheme, u.Scheme) && strings.EqualFold(b.Host, u.Host)
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.Strict || !t.inScope(req.URL) {
		return t.next().RoundTrip(req)
	}
	spec, err := t.Spec()
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, fmt.Errorf("%s=%s but the spec cannot be loaded: %w", EnvValidate, ModeStrict, err)
	}

	var reqBody []byte
	if req.Body != nil {
		reqBody, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("apispec: read request body: %w", err)
		}
	}
	out := cloneRequest(req, reqBody)
	resp, err := t.next().RoundTrip(out)
	if err != nil {
		return nil, err
	}

	op, tpl, pathKnown := spec.Match(req.Method, req.URL.Path)
	opID := ""
	if op != nil {
		opID = op.ID
		t.mu.Lock()
		if t.calls == nil {
			t.calls = map[string]int{}
		}
		t.calls[op.ID]++
		t.mu.Unlock()
	}

	var respBody []byte
	streaming := op != nil && op.SSE && responseMediaType(resp) == "text/event-stream"
	if !streaming {
		respBody, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("apispec: read response body of %s %s: %w", req.Method, req.URL, err)
		}
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
	}

	if problems := spec.check(req, reqBody, resp, respBody, op, tpl, pathKnown); len(problems) > 0 {
		if streaming {
			_ = resp.Body.Close()
		}
		v := &Violation{Method: req.Method, URL: req.URL.String(), Status: resp.StatusCode, OperationID: opID, Problems: problems}
		t.mu.Lock()
		t.violations = append(t.violations, v.Error())
		t.mu.Unlock()
		return nil, v
	}
	return resp, nil
}

// Calls returns how often each operationId was called through t.
func (t *Transport) Calls() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]int, len(t.calls))
	for k, v := range t.calls {
		out[k] = v
	}
	return out
}

// Violations returns the messages of every violation seen so far.
func (t *Transport) Violations() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.violations)
}

// AfterSuite returns the TestMain hook of the operationId coverage rule: when strict validation is
// on and the whole suite ran (not -short, no -run/-skip filter), every operation that is not in
// notImplemented (operationId -> phase) must have been called at least once, notImplemented may
// only name real operationIds, and no violation may have been recorded. Otherwise the exit code
// becomes 1 and the reasons go to stderr.
func (t *Transport) AfterSuite(notImplemented map[string]string) func(code int) int {
	return func(code int) int {
		if !t.Strict || testing.Short() {
			return code
		}
		for _, name := range []string{"test.run", "test.skip"} {
			if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
				fmt.Fprintf(os.Stderr, "apispec: operationId coverage not checked (-%s=%q selects a subset of the suite)\n", strings.TrimPrefix(name, "test."), f.Value.String())
				return code
			}
		}
		spec, err := t.Spec()
		if err != nil {
			fmt.Fprintf(os.Stderr, "apispec: %v\n", err)
			return 1
		}
		failed := false
		if unknown := spec.UnknownIDs(notImplemented); len(unknown) > 0 {
			failed = true
			fmt.Fprintf(os.Stderr, "apispec: the phase allowlist names operationIds that are not in %s: %s\n", spec.Path(), strings.Join(unknown, ", "))
		}
		if missing := spec.Uncovered(t.Calls(), notImplemented); len(missing) > 0 {
			failed = true
			fmt.Fprintf(os.Stderr, "apispec: implemented operations never called by the E2E suite (README §11 requires at least one call each): %s\n", strings.Join(missing, ", "))
		}
		if v := t.Violations(); len(v) > 0 {
			failed = true
			fmt.Fprintf(os.Stderr, "apispec: %d OpenAPI violation(s) recorded during the suite:\n%s\n", len(v), strings.Join(v, "\n"))
		}
		if failed && code == 0 {
			return 1
		}
		return code
	}
}
