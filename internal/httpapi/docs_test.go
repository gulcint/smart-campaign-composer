package httpapi_test

// Interactive API documentation routes, unit level (DECISIONS.md D36; docs/contracts/README.md §11;
// docs/contracts/go-interfaces.md §httpapi "Doküman rotaları (D36)"). There is no REQ id for this
// feature; the tests trace to D36 and README §11. The four `documentation` routes are independent of
// Deps (no DB, LLM or Service), so the router here gets only a clock and a logger.

import (
	"bytes"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/httpapi"
	"github.com/gulcint/smart-campaign-composer/test/testenv/apispec"
)

var (
	docsScriptElemRE    = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	docsScriptSrcRE     = regexp.MustCompile(`(?is)\ssrc\s*=`)
	docsInlineHandlerRE = regexp.MustCompile(`(?is)<[a-z][^>]*\son[a-z]+\s*=`)
	// docsExternalLoadRE finds a <script>/<link>/<img> src or href that names a scheme or a
	// protocol-relative host (another origin).
	docsExternalLoadRE = regexp.MustCompile(`(?is)<(?:script|link|img)\b[^>]*\s(?:src|href)\s*=\s*["']?\s*(?:[a-z][a-z0-9+.-]*:|//)`)
)

// docsOnlyRouter builds the router with no Service, DB, Metrics or LLM provider: go-interfaces.md
// §httpapi says the documentation routes need none of them.
func docsOnlyRouter(t *testing.T) http.Handler {
	t.Helper()
	return httpapi.New(httpapi.Deps{
		Clock:  clock.NewFake(refTime),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func docsContractFile(t *testing.T, rel string) []byte {
	t.Helper()
	p, err := apispec.SpecPath()
	if err != nil {
		t.Fatalf("locate docs/contracts/openapi.yaml: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(p), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read docs/contracts/%s: %v", rel, err)
	}
	return b
}

func docsMediaType(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	ct := rec.Header().Get("Content-Type")
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("Content-Type = %q is not a media type: %v", ct, err)
	}
	return mt
}

// docsCSP is the Content-Security-Policy of docs/contracts/README.md §7 (nginx); go-interfaces.md
// §httpapi "Doküman rotaları (D36)" says the API's documentation responses carry the same policy
// ("nginx'tekiyle aynı"). Compared byte for byte.
const docsCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// assertDocsHeaders: nosniff, X-Frame-Options DENY, Referrer-Policy no-referrer and exactly the
// policy docsCSP (D36, go-interfaces §httpapi, openapi.yaml getApiDocs, README §7). Each header
// appears exactly once.
func assertDocsHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	h := rec.Header()
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		got := h.Values(name)
		if len(got) != 1 || !strings.EqualFold(strings.TrimSpace(got[0]), want) {
			t.Errorf("%s = %q, want exactly one %q", name, got, want)
		}
	}
	csps := h.Values("Content-Security-Policy")
	if len(csps) != 1 {
		t.Fatalf("Content-Security-Policy appears %d times (%q), want exactly once", len(csps), csps)
	}
	if csps[0] != docsCSP {
		t.Errorf("Content-Security-Policy mismatch\n got: %q\nwant: %q", csps[0], docsCSP)
	}
}

// D36, docs/contracts/README.md §11
// Each documentation operation answers 200 with its documented media type and the security headers.
func TestDocsRoutes_ServeWithMediaTypeAndSecurityHeaders(t *testing.T) {
	t.Parallel()
	h := docsOnlyRouter(t)
	tests := []struct {
		path      string
		mediaType string
	}{
		{path: "/docs", mediaType: "text/html"},
		{path: "/docs/swagger-ui-bundle.js", mediaType: "text/javascript"},
		{path: "/docs/swagger-ui-bundle.js.LICENSE.txt", mediaType: "text/plain"},
		{path: "/docs/swagger-ui.css", mediaType: "text/css"},
		{path: "/docs/swagger-initializer.js", mediaType: "text/javascript"},
		{path: "/docs/favicon-32x32.png", mediaType: "image/png"},
		{path: "/docs/LICENSE", mediaType: "text/plain"},
		{path: "/docs/NOTICE", mediaType: "text/plain"},
		{path: "/openapi.yaml", mediaType: "application/yaml"},
		{path: "/schemas/segment-rule.schema.json", mediaType: "application/schema+json"},
		{path: "/schemas/content.schema.json", mediaType: "application/schema+json"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			rec := do(t, h, http.MethodGet, tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200; body: %.300s", tc.path, rec.Code, rec.Body.String())
			}
			if got := docsMediaType(t, rec); got != tc.mediaType {
				t.Fatalf("GET %s media type = %q, want %q", tc.path, got, tc.mediaType)
			}
			if rec.Body.Len() == 0 {
				t.Fatalf("GET %s body is empty", tc.path)
			}
			assertDocsHeaders(t, rec)
		})
	}
}

// D36, docs/contracts/README.md §11
// /openapi.yaml and /schemas/{schema} return the contract files byte for byte (embedded copy ==
// docs/contracts/ by check-contract-copies).
func TestDocsRoutes_ContractFilesAreByteIdentical(t *testing.T) {
	t.Parallel()
	h := docsOnlyRouter(t)
	tests := []struct {
		path string
		file string
	}{
		{path: "/openapi.yaml", file: "openapi.yaml"},
		{path: "/schemas/segment-rule.schema.json", file: "schemas/segment-rule.schema.json"},
		{path: "/schemas/content.schema.json", file: "schemas/content.schema.json"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			want := docsContractFile(t, tc.file)
			rec := do(t, h, http.MethodGet, tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200", tc.path, rec.Code)
			}
			if !bytes.Equal(want, rec.Body.Bytes()) {
				t.Fatalf("GET %s (%d bytes) is not byte-identical to docs/contracts/%s (%d bytes)", tc.path, rec.Body.Len(), tc.file, len(want))
			}
		})
	}
}

// D36, docs/contracts/README.md §11
// The page has no inline script (CSP script-src 'self'), loads nothing from another origin, and is
// started by /docs/swagger-initializer.js, which loads /openapi.yaml and names no absolute address.
func TestDocsPage_NoInlineScriptNoExternalOrigin(t *testing.T) {
	t.Parallel()
	h := docsOnlyRouter(t)
	rec := do(t, h, http.MethodGet, "/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /docs status = %d, want 200", rec.Code)
	}
	page := rec.Body.String()

	t.Run("every script element has src and no body", func(t *testing.T) {
		t.Parallel()
		scripts := docsScriptElemRE.FindAllStringSubmatch(page, -1)
		if len(scripts) == 0 {
			t.Fatal("GET /docs has no <script> element")
		}
		for _, s := range scripts {
			if strings.TrimSpace(s[2]) != "" {
				t.Errorf("inline script body: %.200s", s[2])
			}
			if !docsScriptSrcRE.MatchString(s[1]) {
				t.Errorf("<script%s> has no src", s[1])
			}
		}
	})
	t.Run("no inline event handler", func(t *testing.T) {
		t.Parallel()
		if m := docsInlineHandlerRE.FindString(page); m != "" {
			t.Errorf("inline event handler: %.200s", m)
		}
	})
	t.Run("no file is loaded from another origin", func(t *testing.T) {
		t.Parallel()
		for _, m := range docsExternalLoadRE.FindAllString(page, -1) {
			if strings.Contains(strings.ToLower(m), "data:") {
				continue
			}
			t.Errorf("GET /docs loads from an absolute or protocol-relative URL: %.200s", m)
		}
	})
	t.Run("page references the initializer", func(t *testing.T) {
		t.Parallel()
		if !strings.Contains(page, "swagger-initializer.js") {
			t.Errorf("GET /docs does not reference swagger-initializer.js")
		}
	})
	t.Run("initializer loads openapi.yaml without an absolute URL", func(t *testing.T) {
		t.Parallel()
		js := do(t, h, http.MethodGet, "/docs/swagger-initializer.js").Body.String()
		if !strings.Contains(js, "openapi.yaml") {
			t.Errorf("swagger-initializer.js does not reference openapi.yaml")
		}
		if strings.Contains(js, "://") {
			t.Errorf("swagger-initializer.js contains an absolute URL:\n%s", js)
		}
	})
}

// D36, docs/contracts/README.md §11
// {asset} and {schema} are closed sets (the spec enums): other names are 404 not_found; other methods
// on the four paths are 405 method_not_allowed with Allow: GET.
func TestDocsRoutes_UnknownNamesAndMethods(t *testing.T) {
	t.Parallel()
	h := docsOnlyRouter(t)
	for _, p := range []string{
		"/docs/swagger-ui-bundle.js.map",
		"/docs/swagger-ui.js",
		"/docs/index.html",
		"/docs/Swagger-UI.css",
		"/schemas/segment-rule.tool.json",
		"/schemas/Content.schema.json",
		"/schemas/openapi.yaml",
	} {
		t.Run("GET "+p, func(t *testing.T) {
			t.Parallel()
			rec := send(t, h, http.MethodGet, p, nil, nil)
			assertErrorBody(t, rec, http.StatusNotFound, "not_found", false)
		})
	}
	for _, tc := range []struct{ method, path string }{
		{method: http.MethodPost, path: "/docs"},
		{method: http.MethodPut, path: "/docs/swagger-ui.css"},
		{method: http.MethodDelete, path: "/openapi.yaml"},
		{method: http.MethodPost, path: "/schemas/content.schema.json"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			t.Parallel()
			rec := send(t, h, tc.method, tc.path, nil, nil)
			assertErrorBody(t, rec, http.StatusMethodNotAllowed, "method_not_allowed", false)
			if got := strings.TrimSpace(rec.Header().Get("Allow")); got != "GET" {
				t.Fatalf("Allow = %q, want %q", got, "GET")
			}
		})
	}
}

// D36, docs/contracts/README.md §7 ("Range isteği de 200 ve tam gövde döner (API 206/416 üretmez)")
// A Range request on a documentation route answers 200 with the full body and the plain GET's
// Content-Type (never multipart/byteranges); Content-Length, when set, is the full size; no
// Content-Range; no Accept-Ranges advertising byte ranges.
func TestDocsRoutes_RangeRequestsGetTheFullBody(t *testing.T) {
	t.Parallel()
	h := docsOnlyRouter(t)
	paths := []string{"/openapi.yaml", "/docs/swagger-ui.css", "/docs/favicon-32x32.png", "/docs", "/schemas/content.schema.json"}
	ranges := []string{"bytes=0-9", "bytes=99999999-", "bytes=0-0,5-9", "bytes=-1"}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			plain := do(t, h, http.MethodGet, p)
			if plain.Code != http.StatusOK || plain.Body.Len() == 0 {
				t.Fatalf("GET %s status = %d (%d bytes), want 200 with a body", p, plain.Code, plain.Body.Len())
			}
			full := plain.Body.Bytes()
			wantCT := plain.Header().Get("Content-Type")
			for _, r := range ranges {
				t.Run("Range "+r, func(t *testing.T) {
					t.Parallel()
					rec := send(t, h, http.MethodGet, p, nil, map[string]string{"Range": r})
					if rec.Code != http.StatusOK {
						t.Fatalf("status = %d, want 200 (no 206/416)", rec.Code)
					}
					ct := rec.Header().Get("Content-Type")
					if mt, _, err := mime.ParseMediaType(ct); err == nil && strings.HasPrefix(mt, "multipart/") {
						t.Fatalf("Content-Type = %q: a multipart range response", ct)
					}
					if ct != wantCT {
						t.Errorf("Content-Type = %q, want %q (the plain GET's)", ct, wantCT)
					}
					if !bytes.Equal(full, rec.Body.Bytes()) {
						t.Errorf("body (%d bytes) is not the full body (%d bytes)", rec.Body.Len(), len(full))
					}
					if cl := rec.Header().Values("Content-Length"); len(cl) > 0 {
						if len(cl) != 1 || strings.TrimSpace(cl[0]) != strconv.Itoa(len(full)) {
							t.Errorf("Content-Length = %q, want exactly one %d", cl, len(full))
						}
					}
					if cr := rec.Header().Values("Content-Range"); len(cr) > 0 {
						t.Errorf("Content-Range = %q on a 200 full-body response", cr)
					}
					for _, v := range rec.Header().Values("Accept-Ranges") {
						if strings.Contains(strings.ToLower(v), "bytes") {
							t.Errorf("Accept-Ranges = %q although ranges are not served", v)
						}
					}
				})
			}
		})
	}
}

// D36, docs/contracts/README.md §11; go-interfaces.md §httpapi ("{asset} ve {schema} kapalı
// kümelerdir (spec'teki enum), dışındaki ad 404 not_found"); openapi.yaml /docs/{asset},
// /schemas/{schema}
// The served names are exactly the spec enums: the enum equals the documented list, every enum value
// answers 200 with a body, and names outside it answer 404 `not_found`.
func TestDocsRoutes_ServedNamesEqualTheSpecEnums(t *testing.T) {
	t.Parallel()
	h := docsOnlyRouter(t)
	path, err := apispec.SpecPath()
	if err != nil {
		t.Fatalf("locate openapi.yaml: %v", err)
	}
	spec, err := apispec.Load(path)
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	tests := []struct {
		template, param, prefix string
		names                   []string
		notFound                []string
	}{
		{
			template: "/docs/{asset}", param: "asset", prefix: "/docs/",
			names: []string{
				"swagger-ui-bundle.js", "swagger-ui-bundle.js.LICENSE.txt", "swagger-ui.css",
				"swagger-initializer.js", "favicon-32x32.png", "LICENSE", "NOTICE",
			},
			notFound: []string{
				"index.html", "swagger-ui-bundle.js.map", "swagger-ui.js", "..%2Fopenapi.yaml", "",
				"SWAGGER-UI.CSS", "Swagger-UI-Bundle.js", "swagger-initializer.JS", "Favicon-32x32.png",
				"license", "Notice", "SWAGGER-UI-BUNDLE.JS.LICENSE.TXT",
			},
		},
		{
			template: "/schemas/{schema}", param: "schema", prefix: "/schemas/",
			names: []string{"segment-rule.schema.json", "content.schema.json"},
			notFound: []string{
				"", "CONTENT.SCHEMA.JSON", "Segment-Rule.schema.json", "..%2Fopenapi.yaml",
				"llm%2Fcontent.tool.json", "content.tool.json",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.template, func(t *testing.T) {
			t.Parallel()
			enum, err := spec.PathEnum(tc.template, tc.param)
			if err != nil {
				t.Fatalf("read the {%s} enum: %v", tc.param, err)
			}
			if diff := cmp.Diff(slices.Sorted(slices.Values(tc.names)), enum); diff != "" {
				t.Fatalf("{%s} enum of openapi.yaml vs the documented names (-names +enum):\n%s", tc.param, diff)
			}
			for _, name := range enum {
				t.Run("serves "+name, func(t *testing.T) {
					t.Parallel()
					rec := do(t, h, http.MethodGet, tc.prefix+name)
					if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
						t.Fatalf("GET %s%s status = %d (%d bytes), want 200 with a body", tc.prefix, name, rec.Code, rec.Body.Len())
					}
				})
			}
			for _, name := range tc.notFound {
				label := name
				if label == "" {
					label = "(empty)"
				}
				t.Run("404 "+label, func(t *testing.T) {
					t.Parallel()
					rec := send(t, h, http.MethodGet, tc.prefix+name, nil, nil)
					assertErrorBody(t, rec, http.StatusNotFound, "not_found", false)
				})
			}
		})
	}
}
