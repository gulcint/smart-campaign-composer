package e2e_test

// Interactive API documentation (DECISIONS.md D36; docs/contracts/README.md §11 "Etkileşimli doküman";
// docs/contracts/go-interfaces.md §httpapi "Doküman rotaları (D36)"; openapi.yaml tag `documentation`:
// getApiDocs, getApiDocsAsset, getOpenApiSpec, getContractSchema). There is no REQ id for this
// feature; the tests trace to D36 and README §11. Every call goes through the strict E2E client, so
// each response is also validated against openapi.yaml and each operationId is recorded for the
// coverage gate.

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/test/testenv/apispec"
)

// docsAssets is the closed {asset} set of openapi.yaml /docs/{asset} with the media type its
// description assigns: `.js` => text/javascript, `.css` => text/css, `.png` => image/png,
// LICENSE, NOTICE and *.LICENSE.txt => text/plain.
var docsAssets = []struct {
	name      string
	mediaType string
}{
	{name: "swagger-ui-bundle.js", mediaType: "text/javascript"},
	{name: "swagger-ui-bundle.js.LICENSE.txt", mediaType: "text/plain"},
	{name: "swagger-ui.css", mediaType: "text/css"},
	{name: "swagger-initializer.js", mediaType: "text/javascript"},
	{name: "favicon-32x32.png", mediaType: "image/png"},
	{name: "LICENSE", mediaType: "text/plain"},
	{name: "NOTICE", mediaType: "text/plain"},
}

// docsSchemas is the closed {schema} set of openapi.yaml /schemas/{schema}.
var docsSchemas = []string{"segment-rule.schema.json", "content.schema.json"}

var (
	// loadTagRE finds the elements whose src/href the browser fetches.
	loadTagRE = regexp.MustCompile(`(?is)<(script|link|img)\b([^>]*)>`)
	// loadAttrRE reads a src or href attribute (double, single or un-quoted value).
	loadAttrRE = regexp.MustCompile(`(?is)\s(src|href)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	// baseHrefRE reads <base href>, which changes how relative references resolve.
	baseHrefRE = regexp.MustCompile(`(?is)<base\b[^>]*\shref\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	// scriptElemRE captures each <script> element's attributes and body.
	scriptElemRE = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	// scriptSrcAttrRE reports whether a <script> start tag has a src attribute.
	scriptSrcAttrRE = regexp.MustCompile(`(?is)\ssrc\s*=`)
	// inlineHandlerRE finds inline event handler attributes (onload=, onclick=, ...): inline script too.
	inlineHandlerRE = regexp.MustCompile(`(?is)<[a-z][^>]*\son[a-z]+\s*=`)
	// specFileRefRE finds the relative file $refs of openapi.yaml (e.g. `$ref: './schemas/x.schema.json#/$defs/y'`;
	// quotes, the leading `./` and the fragment are optional) and captures the file reference without the
	// fragment. Anchored on `$ref:` so prose mentioning schemas/... does not count.
	specFileRefRE = regexp.MustCompile(`\$ref:\s*['"]?((?:\./)?schemas/[^'"#\s,}]+)`)
)

// contractsDir is the directory of the spec the E2E validator reads (OPENAPI_SPEC_PATH or
// <module root>/docs/contracts), i.e. docs/contracts/.
func contractsDir(t *testing.T) string {
	t.Helper()
	p, err := apispec.SpecPath()
	if err != nil {
		t.Fatalf("locate docs/contracts/openapi.yaml: %v", err)
	}
	return filepath.Dir(p)
}

func readContractFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(contractsDir(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read docs/contracts/%s: %v", rel, err)
	}
	return b
}

// assertMediaType checks the Content-Type media type (parameters such as charset are allowed).
func assertMediaType(t *testing.T, what string, hdr http.Header, want string) {
	t.Helper()
	ct := hdr.Get("Content-Type")
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("%s Content-Type = %q is not a media type: %v", what, ct, err)
	}
	if mt != want {
		t.Fatalf("%s Content-Type = %q, want media type %s", what, ct, want)
	}
}

// assertDocsSecurityHeaders checks the headers go-interfaces.md §httpapi and openapi.yaml getApiDocs
// require on the documentation responses: nosniff, X-Frame-Options DENY, Referrer-Policy no-referrer
// and a CSP that allows only same-origin scripts and connections (D36: `script-src 'self'`,
// `connect-src 'self'`, not relaxed, so no 'unsafe-eval'). Each header appears exactly once.
func assertDocsSecurityHeaders(t *testing.T, what string, hdr http.Header) {
	t.Helper()
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for name, value := range want {
		got := hdr.Values(name)
		if len(got) != 1 {
			t.Errorf("%s: header %s appears %d times (%q), want exactly once", what, name, len(got), got)
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(got[0]), value) {
			t.Errorf("%s: %s = %q, want %q", what, name, got[0], value)
		}
	}
	csps := hdr.Values("Content-Security-Policy")
	if len(csps) != 1 {
		t.Fatalf("%s: Content-Security-Policy appears %d times (%q), want exactly once", what, len(csps), csps)
	}
	dirs := parseCSP(csps[0])
	for _, name := range []string{"script-src", "connect-src"} {
		got, ok := dirs[name]
		if !ok {
			t.Errorf("%s: CSP %q has no %s directive", what, csps[0], name)
			continue
		}
		if diff := cmp.Diff([]string{"'self'"}, got); diff != "" {
			t.Errorf("%s: CSP %s must allow only the same origin (-want +got):\n%s", what, name, diff)
		}
	}
	if strings.Contains(strings.ToLower(csps[0]), "'unsafe-eval'") {
		t.Errorf("%s: CSP %q contains 'unsafe-eval'; D36 says the policy was not relaxed", what, csps[0])
	}
}

// parseCSP splits a policy into directive name (lower case) -> source tokens (sources lower-cased
// only for keywords in quotes, so 'self' compares exactly).
func parseCSP(policy string) map[string][]string {
	out := map[string][]string{}
	for _, d := range strings.Split(policy, ";") {
		f := strings.Fields(d)
		if len(f) == 0 {
			continue
		}
		name := strings.ToLower(f[0])
		if _, dup := out[name]; dup {
			continue // a browser uses the first occurrence of a directive
		}
		var srcs []string
		for _, s := range f[1:] {
			if strings.HasPrefix(s, "'") {
				s = strings.ToLower(s)
			}
			srcs = append(srcs, s)
		}
		out[name] = srcs
	}
	return out
}

// pageLoads returns the src/href values of <script>, <link> and <img> in page.
func pageLoads(page string) []string {
	var out []string
	for _, tag := range loadTagRE.FindAllStringSubmatch(page, -1) {
		for _, a := range loadAttrRE.FindAllStringSubmatch(tag[2], -1) {
			out = append(out, a[2]+a[3]+a[4])
		}
	}
	return out
}

// D36, docs/contracts/README.md §11
// GET /docs (getApiDocs) is the Swagger UI page: 200 HTML with the security headers, no inline script
// (the UI starts from /docs/swagger-initializer.js), every loaded file comes from the same origin and
// answers 200, and the page loads the Swagger UI bundle, its stylesheet and the initializer.
func TestE2E_DocsPage(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	status, hdr, body := doJSON(t, http.MethodGet, api+"/docs", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /docs status = %d, want 200; body: %.300s", status, body)
	}
	assertMediaType(t, "GET /docs", hdr, "text/html")
	assertDocsSecurityHeaders(t, "GET /docs", hdr)
	page := string(body)

	t.Run("no inline script", func(t *testing.T) {
		scripts := scriptElemRE.FindAllStringSubmatch(page, -1)
		if len(scripts) == 0 {
			t.Fatalf("GET /docs has no <script> element; the UI is started by /docs/swagger-initializer.js")
		}
		for _, s := range scripts {
			if strings.TrimSpace(s[2]) != "" {
				t.Errorf("GET /docs has an inline script body (CSP script-src 'self' blocks it): %.200s", s[2])
			}
			if !scriptSrcAttrRE.MatchString(s[1]) {
				t.Errorf("GET /docs has a <script%s> without src", s[1])
			}
		}
		if m := inlineHandlerRE.FindString(page); m != "" {
			t.Errorf("GET /docs has an inline event handler (inline script): %.200s", m)
		}
	})

	pageURL, err := url.Parse(api + "/docs")
	if err != nil {
		t.Fatalf("parse page URL: %v", err)
	}
	base := pageURL
	if m := baseHrefRE.FindStringSubmatch(page); m != nil {
		b, err := url.Parse(m[1] + m[2])
		if err != nil {
			t.Fatalf("GET /docs <base href> %q is not a URL: %v", m[1]+m[2], err)
		}
		if b.Scheme != "" || b.Host != "" {
			t.Fatalf("GET /docs <base href> %q points to another origin", m[1]+m[2])
		}
		base = pageURL.ResolveReference(b)
	}

	var loadedPaths []string
	t.Run("every loaded file is same-origin and answers 200", func(t *testing.T) {
		loads := pageLoads(page)
		if len(loads) == 0 {
			t.Fatalf("GET /docs loads no file")
		}
		for _, ref := range loads {
			if strings.HasPrefix(strings.ToLower(ref), "data:") || strings.HasPrefix(ref, "#") {
				continue
			}
			u, err := url.Parse(ref)
			if err != nil {
				t.Errorf("GET /docs reference %q is not a URL: %v", ref, err)
				continue
			}
			if u.Scheme != "" || u.Host != "" {
				t.Errorf("GET /docs loads %q from an external origin (D36: no CDN, no absolute server address)", ref)
				continue
			}
			abs := base.ResolveReference(u)
			loadedPaths = append(loadedPaths, abs.Path)
			st, _, b := doJSON(t, http.MethodGet, abs.String(), nil, nil)
			if st != http.StatusOK {
				t.Errorf("GET /docs references %q (resolves to %s), which answers %d: %.200s", ref, abs.Path, st, b)
			}
		}
	})

	t.Run("loads the bundle, the stylesheet and the initializer from /docs/", func(t *testing.T) {
		for _, want := range []string{"/docs/swagger-ui-bundle.js", "/docs/swagger-ui.css", "/docs/swagger-initializer.js"} {
			if !slices.Contains(loadedPaths, want) {
				t.Errorf("GET /docs does not load %s; loads resolve to %v", want, loadedPaths)
			}
		}
	})
}

// D36, docs/contracts/README.md §11
// Every documented asset of GET /docs/{asset} (getApiDocsAsset) answers 200 with the media type the
// spec assigns, a non-empty body and the security headers.
func TestE2E_DocsAssets(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	for _, tc := range docsAssets {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			what := "GET /docs/" + tc.name
			status, hdr, body := doJSON(t, http.MethodGet, api+"/docs/"+tc.name, nil, nil)
			if status != http.StatusOK {
				t.Fatalf("%s status = %d, want 200; body: %.300s", what, status, body)
			}
			assertMediaType(t, what, hdr, tc.mediaType)
			assertDocsSecurityHeaders(t, what, hdr)
			if len(body) == 0 {
				t.Fatalf("%s body is empty", what)
			}
			if tc.mediaType == "image/png" && !bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
				t.Fatalf("%s is served as image/png but is not a PNG file", what)
			}
		})
	}
}

// D36, docs/contracts/README.md §11
// The page initializer loads this contract from /openapi.yaml and carries no absolute server address
// (D36: "mutlak sunucu adresi yoktur"; openapi.yaml getApiDocs: "loads ... this contract from /openapi.yaml").
func TestE2E_DocsInitializerLoadsTheEmbeddedContract(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	status, _, body := doJSON(t, http.MethodGet, api+"/docs/swagger-initializer.js", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /docs/swagger-initializer.js status = %d, want 200", status)
	}
	if !bytes.Contains(body, []byte("openapi.yaml")) {
		t.Errorf("swagger-initializer.js does not reference openapi.yaml:\n%s", body)
	}
	if bytes.Contains(body, []byte("://")) {
		t.Errorf("swagger-initializer.js contains an absolute URL; the page must use its own origin:\n%s", body)
	}
}

// D36, docs/contracts/README.md §11
// GET /openapi.yaml (getOpenApiSpec) is byte-identical to docs/contracts/openapi.yaml, served as
// application/yaml with the security headers.
func TestE2E_OpenAPISpecIsTheContract(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	want := readContractFile(t, "openapi.yaml")
	status, hdr, body := doJSON(t, http.MethodGet, api+"/openapi.yaml", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /openapi.yaml status = %d, want 200; body: %.300s", status, body)
	}
	assertMediaType(t, "GET /openapi.yaml", hdr, "application/yaml")
	assertDocsSecurityHeaders(t, "GET /openapi.yaml", hdr)
	if !bytes.Equal(want, body) {
		t.Fatalf("GET /openapi.yaml (%d bytes) is not byte-identical to docs/contracts/openapi.yaml (%d bytes)", len(body), len(want))
	}
}

// D36, docs/contracts/README.md §11
// GET /schemas/{schema} (getContractSchema) serves each schema byte-identical to
// docs/contracts/schemas/{schema}, as application/schema+json (a JSON object) with the security headers.
func TestE2E_ContractSchemasAreTheContractFiles(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	for _, name := range docsSchemas {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			what := "GET /schemas/" + name
			want := readContractFile(t, "schemas/"+name)
			status, hdr, body := doJSON(t, http.MethodGet, api+"/schemas/"+name, nil, nil)
			if status != http.StatusOK {
				t.Fatalf("%s status = %d, want 200; body: %.300s", what, status, body)
			}
			assertMediaType(t, what, hdr, "application/schema+json")
			assertDocsSecurityHeaders(t, what, hdr)
			if !bytes.Equal(want, body) {
				t.Fatalf("%s (%d bytes) is not byte-identical to docs/contracts/schemas/%s (%d bytes)", what, len(body), name, len(want))
			}
			var obj map[string]any
			if err := json.Unmarshal(body, &obj); err != nil {
				t.Fatalf("%s is not a JSON object: %v", what, err)
			}
		})
	}
}

// D36 (deviation 2), docs/contracts/README.md §11
// The relative $refs of the served spec resolve, against /openapi.yaml, to /schemas/{schema}, and each
// of those URLs serves the referenced contract file — so Swagger UI can render the external schemas.
func TestE2E_OpenAPISpecRefsResolveToSchemas(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	status, _, spec := doJSON(t, http.MethodGet, api+"/openapi.yaml", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /openapi.yaml status = %d, want 200", status)
	}
	refs := map[string]bool{}
	for _, m := range specFileRefRE.FindAllSubmatch(spec, -1) {
		refs[string(m[1])] = true
	}
	if len(refs) == 0 {
		t.Fatalf("served openapi.yaml has no relative schemas/*.schema.json $ref")
	}
	specURL, err := url.Parse(api + "/openapi.yaml")
	if err != nil {
		t.Fatalf("parse spec URL: %v", err)
	}
	for ref := range refs {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(ref)
			if err != nil {
				t.Fatalf("$ref %q is not a relative URL: %v", ref, err)
			}
			abs := specURL.ResolveReference(u)
			if !strings.HasPrefix(abs.Path, "/schemas/") {
				t.Fatalf("$ref %q resolves to %s, want /schemas/{schema}", ref, abs.Path)
			}
			st, _, body := doJSON(t, http.MethodGet, abs.String(), nil, nil)
			if st != http.StatusOK {
				t.Fatalf("GET %s (from $ref %q) status = %d, want 200", abs.Path, ref, st)
			}
			if want := readContractFile(t, ref); !bytes.Equal(want, body) {
				t.Fatalf("GET %s is not byte-identical to docs/contracts/%s", abs.Path, ref)
			}
		})
	}
}

// D36, docs/contracts/README.md §11
// {asset} and {schema} are closed sets: any other name answers 404 `not_found` in the README §5 error
// shape. Names include files that exist in swagger-ui-dist but are not embedded (D36: no .map files).
func TestE2E_DocsUnknownNamesAre404(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	paths := []string{
		"/docs/swagger-ui-bundle.js.map",
		"/docs/swagger-ui.js",
		"/docs/swagger-ui-standalone-preset.js",
		"/docs/index.html",
		"/docs/Swagger-UI.css",
		"/docs/openapi.yaml",
		"/schemas/segment-rule.tool.json",
		"/schemas/openapi.yaml",
		"/schemas/Content.schema.json",
		"/schemas/content.schema",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, http.MethodGet, api+p, nil, nil)
			assertError(t, status, hdr, body, http.StatusNotFound, "not_found", false)
		})
	}
}

// D36, docs/contracts/README.md §11
// The documentation paths define only GET: any other method answers 405 `method_not_allowed` with
// `Allow: GET` (openapi.yaml components.responses.MethodNotAllowed: the methods the path defines).
func TestE2E_DocsRoutesAreGetOnly(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	tests := []struct {
		method, path string
	}{
		{method: http.MethodPost, path: "/docs"},
		{method: http.MethodDelete, path: "/docs"},
		{method: http.MethodPut, path: "/docs/swagger-ui.css"},
		{method: http.MethodPost, path: "/docs/swagger-initializer.js"},
		{method: http.MethodPut, path: "/openapi.yaml"},
		{method: http.MethodDelete, path: "/openapi.yaml"},
		{method: http.MethodPost, path: "/schemas/content.schema.json"},
		{method: http.MethodPatch, path: "/schemas/segment-rule.schema.json"},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, tc.method, api+tc.path, nil, nil)
			assertError(t, status, hdr, body, http.StatusMethodNotAllowed, "method_not_allowed", false)
			if got := strings.TrimSpace(hdr.Get("Allow")); got != "GET" {
				t.Fatalf("Allow = %q, want %q", got, "GET")
			}
		})
	}
}
