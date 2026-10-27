package e2e_test

// nginx-level tests of the documentation and system paths (DECISIONS.md D36, deviation 3;
// docs/contracts/README.md §7, paragraph "E2E_WEB_URL (nginx düzeyi testler)"; README §11
// "Etkileşimli doküman"). There is no REQ id for this feature; the tests trace to D36 and README §7.
//
// README §7 states, for the web service at E2E_WEB_URL (compose.test.yml sets it to http://web:8080):
//   - the whole /docs prefix (any depth, case-insensitive), the whole /schemas prefix, /openapi.yaml,
//     /healthz, /readyz and /metrics are forwarded to the API and the API's answer comes back
//     unchanged (so /Docs, /DOCS, /docs/ get the API's JSON 404 `not_found`, never the SPA index.html);
//   - every other path is the SPA (index.html, 200);
//   - on forwarded and SPA paths alike: X-Content-Type-Options: nosniff, X-Frame-Options: DENY,
//     Referrer-Policy: no-referrer and one Content-Security-Policy (docsCSP), each exactly once;
//   - a Range request on a forwarded path answers 200 with the full body;
//   - with Accept-Encoding: gzip, text bodies of at least 1024 bytes MAY come gzip-encoded with
//     Vary: Accept-Encoding, and the decoded body equals the API's body.
//
// Requests to the web origin go through webClient: they are not validated against openapi.yaml (the
// spec describes the API; the forwarded answers are compared with E2E_API_URL instead), redirects are
// not followed (the first answer is the one checked), and Accept-Encoding is set explicitly where
// encoding matters so the Go transport does not decode transparently.

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var webClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// webURL returns E2E_WEB_URL. Integration layer: skipped under -short; skipped when E2E_WEB_URL is
// unset (the make test container sets it, README §7).
func webURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e: needs the compose.test.yml stack (skipped under -short)")
	}
	u := os.Getenv("E2E_WEB_URL")
	if u == "" {
		t.Skip("E2E_WEB_URL is not set; nginx-level tests run through `make test` (docs/contracts/README.md §7)")
	}
	return strings.TrimRight(u, "/")
}

// webGet sends GET url through webClient with the given headers.
func webGet(t *testing.T, url string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest(GET %s): %v", url, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := webClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of GET %s: %v", url, err)
	}
	return resp.StatusCode, resp.Header, body
}

// apiIdentityGet fetches path from the API (strict client) without content coding and requires 200.
func apiIdentityGet(t *testing.T, api, path string) (http.Header, []byte) {
	t.Helper()
	status, hdr, body := doJSON(t, http.MethodGet, api+path, nil, identityEncoding)
	if status != http.StatusOK {
		t.Fatalf("API GET %s status = %d, want 200; body: %.200s", path, status, body)
	}
	return hdr, body
}

// D36, docs/contracts/README.md §7 and §11
// The documentation paths are forwarded to the API: 200 with the API's media type and body
// (/openapi.yaml and /schemas/* byte-identical to docs/contracts/), and the security headers each
// exactly once.
func TestWeb_DocumentationPathsAreForwarded(t *testing.T) {
	t.Parallel()
	web := webURL(t)
	api := baseURL(t, "E2E_API_URL")
	tests := []struct {
		path         string
		mediaType    string
		contractFile string // "" => compare with the API's body
	}{
		{path: "/docs", mediaType: "text/html"},
		{path: "/docs/swagger-ui.css", mediaType: "text/css"},
		{path: "/docs/swagger-initializer.js", mediaType: "text/javascript"},
		{path: "/openapi.yaml", mediaType: "application/yaml", contractFile: "openapi.yaml"},
		{path: "/schemas/content.schema.json", mediaType: "application/schema+json", contractFile: "schemas/content.schema.json"},
		{path: "/schemas/segment-rule.schema.json", mediaType: "application/schema+json", contractFile: "schemas/segment-rule.schema.json"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			what := "web GET " + tc.path
			status, hdr, body := webGet(t, web+tc.path, identityEncoding)
			if status != http.StatusOK {
				t.Fatalf("%s status = %d, want 200; body: %.300s", what, status, body)
			}
			assertMediaType(t, what, hdr, tc.mediaType)
			assertDocsSecurityHeaders(t, what, hdr)
			var want []byte
			if tc.contractFile != "" {
				want = readContractFile(t, tc.contractFile)
			} else {
				_, want = apiIdentityGet(t, api, tc.path)
			}
			if !bytes.Equal(want, body) {
				t.Fatalf("%s body (%d bytes) differs from the expected body (%d bytes)", what, len(body), len(want))
			}
		})
	}
}

// D36 (deviation 3), docs/contracts/README.md §7; metrics.md (format)
// /healthz, /readyz and /metrics are forwarded to the API: the health bodies of metrics.md §3, and
// /metrics in the Prometheus text format with the API's own Content-Type; security headers on each.
func TestWeb_HealthAndMetricsAreForwarded(t *testing.T) {
	t.Parallel()
	web := webURL(t)
	api := baseURL(t, "E2E_API_URL")
	health := []struct {
		path string
		want map[string]any
	}{
		{path: "/healthz", want: map[string]any{"status": "ok"}},
		{path: "/readyz", want: map[string]any{"status": "ready", "checks": map[string]any{"database": "ok"}}},
	}
	for _, tc := range health {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			what := "web GET " + tc.path
			status, hdr, body := webGet(t, web+tc.path, nil)
			if status != http.StatusOK {
				t.Fatalf("%s status = %d, want 200; body: %.300s", what, status, body)
			}
			assertMediaType(t, what, hdr, "application/json")
			assertDocsSecurityHeaders(t, what, hdr)
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("%s body is not JSON (%v): %.300s", what, err, body)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("%s body mismatch (-want +got):\n%s", what, diff)
			}
		})
	}
	t.Run("/metrics", func(t *testing.T) {
		t.Parallel()
		what := "web GET /metrics"
		status, hdr, body := webGet(t, web+"/metrics", nil)
		if status != http.StatusOK {
			t.Fatalf("%s status = %d, want 200; body: %.300s", what, status, body)
		}
		assertDocsSecurityHeaders(t, what, hdr)
		ct := hdr.Get("Content-Type")
		mt, params, err := mime.ParseMediaType(ct)
		if err != nil || mt != "text/plain" || params["version"] != "0.0.4" {
			t.Fatalf("%s Content-Type = %q, want text/plain with version=0.0.4 (metrics.md)", what, ct)
		}
		apiHdr, _ := apiIdentityGet(t, api, "/metrics")
		if got, want := ct, apiHdr.Get("Content-Type"); got != want {
			t.Fatalf("%s Content-Type = %q, want the API's %q (README §7: answer unchanged)", what, got, want)
		}
	})
}

// D36 (deviation 3), docs/contracts/README.md §7
// Misses under the /docs and /schemas prefixes (trailing slash, any depth, an encoded slash, unknown
// names) and case variants of the prefixes reach the API and get its JSON 404 `not_found` in the
// README §5 shape, never the SPA's index.html; the security headers appear exactly once.
func TestWeb_DocumentationPrefixMissesAreAPI404s(t *testing.T) {
	t.Parallel()
	web := webURL(t)
	paths := []string{
		"/docs/",
		"/Docs",
		"/DOCS",
		"/docs/a%2Fb",
		"/docs/a/b/c",
		"/docs/index.html",
		"/Docs/swagger-ui.css",
		"/schemas/",
		"/schemas/x",
		"/Schemas/content.schema.json",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := webGet(t, web+p, nil)
			assertError(t, status, hdr, body, http.StatusNotFound, "not_found", false)
			assertDocsSecurityHeaders(t, "web GET "+p, hdr)
		})
	}
}

// docs/contracts/README.md §7
// Every path that is not forwarded is the SPA: 200 HTML, the same index.html as "/", not the
// Swagger UI page; the security headers appear exactly once.
func TestWeb_UnknownRoutesServeTheSPA(t *testing.T) {
	t.Parallel()
	web := webURL(t)
	api := baseURL(t, "E2E_API_URL")

	status, hdr, index := webGet(t, web+"/", nil)
	if status != http.StatusOK {
		t.Fatalf("web GET / status = %d, want 200; body: %.300s", status, index)
	}
	assertMediaType(t, "web GET /", hdr, "text/html")
	assertDocsSecurityHeaders(t, "web GET /", hdr)
	if len(index) == 0 {
		t.Fatal("web GET / body is empty")
	}
	_, docsPage := apiIdentityGet(t, api, "/docs")
	if bytes.Equal(index, docsPage) {
		t.Fatal("web GET / returns the Swagger UI page; want the SPA index.html")
	}

	for _, p := range []string{"/no-such-page", "/campaigns/not-a-real-route/deeper"} {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			what := "web GET " + p
			status, hdr, body := webGet(t, web+p, nil)
			if status != http.StatusOK {
				t.Fatalf("%s status = %d, want 200 (SPA); body: %.300s", what, status, body)
			}
			assertMediaType(t, what, hdr, "text/html")
			assertDocsSecurityHeaders(t, what, hdr)
			if !bytes.Equal(index, body) {
				t.Fatalf("%s (%d bytes) is not the SPA index.html served at / (%d bytes)", what, len(body), len(index))
			}
		})
	}
}

// D36, docs/contracts/README.md §7 (Range)
// Through nginx a Range request on a forwarded path also answers 200 with the full body (never
// 206/416/multipart), with the API's Content-Type.
func TestWeb_RangeRequestsGetTheFullBody(t *testing.T) {
	t.Parallel()
	web := webURL(t)
	api := baseURL(t, "E2E_API_URL")
	for _, p := range []string{"/openapi.yaml", "/docs/swagger-ui.css", "/docs", "/schemas/content.schema.json"} {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			apiHdr, full := apiIdentityGet(t, api, p)
			for _, r := range docsRangeHeaders {
				t.Run("Range "+r, func(t *testing.T) {
					t.Parallel()
					st, h, b := webGet(t, web+p, withHeader(identityEncoding, "Range", r))
					assertRangeIgnored(t, "web GET "+p+" (Range: "+r+")", st, h, b, apiHdr.Get("Content-Type"), full)
				})
			}
		})
	}
}

// varyHas reports whether any Vary value lists name (comma-separated, case-insensitive).
func varyHas(hdr http.Header, name string) bool {
	for _, v := range hdr.Values("Vary") {
		for _, f := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(f), name) {
				return true
			}
		}
	}
	return false
}

// D36, docs/contracts/README.md §7 (gzip)
// With Accept-Encoding: gzip a large text body is either sent unencoded or gzip-encoded with
// Vary: Accept-Encoding; decoded, it equals the API's body. No other coding is used (the client
// accepted only gzip). With Accept-Encoding: identity the body is not encoded.
func TestWeb_GzipBodyEqualsTheAPIBody(t *testing.T) {
	t.Parallel()
	web := webURL(t)
	api := baseURL(t, "E2E_API_URL")
	for _, p := range []string{"/docs/swagger-ui-bundle.js", "/docs/swagger-ui.css", "/openapi.yaml"} {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			_, want := apiIdentityGet(t, api, p)
			if len(want) < 1024 {
				t.Fatalf("API GET %s body is %d bytes; this test needs a text body of at least 1024 bytes", p, len(want))
			}

			t.Run("Accept-Encoding gzip", func(t *testing.T) {
				t.Parallel()
				what := "web GET " + p + " (Accept-Encoding: gzip)"
				status, hdr, raw := webGet(t, web+p, map[string]string{"Accept-Encoding": "gzip"})
				if status != http.StatusOK {
					t.Fatalf("%s status = %d, want 200", what, status)
				}
				assertDocsSecurityHeaders(t, what, hdr)
				ce := hdr.Values("Content-Encoding")
				switch {
				case len(ce) == 0:
					if !bytes.Equal(want, raw) {
						t.Fatalf("%s unencoded body (%d bytes) differs from the API's (%d bytes)", what, len(raw), len(want))
					}
				case len(ce) == 1 && strings.EqualFold(strings.TrimSpace(ce[0]), "gzip"):
					if !varyHas(hdr, "Accept-Encoding") {
						t.Errorf("%s is gzip-encoded but Vary = %q lacks Accept-Encoding", what, hdr.Values("Vary"))
					}
					zr, err := gzip.NewReader(bytes.NewReader(raw))
					if err != nil {
						t.Fatalf("%s says gzip but the body is not gzip: %v", what, err)
					}
					got, err := io.ReadAll(zr)
					if err != nil {
						t.Fatalf("%s gzip body does not decode: %v", what, err)
					}
					if !bytes.Equal(want, got) {
						t.Fatalf("%s decoded body (%d bytes) differs from the API's (%d bytes)", what, len(got), len(want))
					}
				default:
					t.Fatalf("%s Content-Encoding = %q; the client accepted only gzip", what, ce)
				}
			})

			t.Run("Accept-Encoding identity", func(t *testing.T) {
				t.Parallel()
				what := "web GET " + p + " (Accept-Encoding: identity)"
				status, hdr, raw := webGet(t, web+p, identityEncoding)
				if status != http.StatusOK {
					t.Fatalf("%s status = %d, want 200", what, status)
				}
				for _, v := range hdr.Values("Content-Encoding") {
					if !strings.EqualFold(strings.TrimSpace(v), "identity") {
						t.Fatalf("%s Content-Encoding = %q; the client asked for identity", what, hdr.Values("Content-Encoding"))
					}
				}
				if !bytes.Equal(want, raw) {
					t.Fatalf("%s body (%d bytes) differs from the API's (%d bytes)", what, len(raw), len(want))
				}
			})
		})
	}
}
