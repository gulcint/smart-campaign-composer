package e2e_test

// Hardening of the documentation routes at the API (DECISIONS.md D36; docs/contracts/README.md §7,
// paragraph "E2E_WEB_URL": "Range isteği de 200 ve tam gövde döner (API 206/416 üretmez)"; README §11
// "Etkileşimli doküman"; go-interfaces.md §httpapi "Doküman rotaları (D36)": "{asset} ve {schema}
// kapalı kümelerdir (spec'teki enum), dışındaki ad 404 not_found"; openapi.yaml getApiDocsAsset:
// "Any other name => 404 not_found"). There is no REQ id for this feature; the tests trace to D36 and
// README §7/§11. Every call goes through the strict E2E client, so a 206, 416, 301 or any other
// status the spec does not list for the operation also fails the call itself.

import (
	"bytes"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// docsRangeHeaders are Range requests the documentation paths must ignore: a plain first-bytes
// range, a range past the end (would be 416), a multi-range (would be multipart/byteranges) and a
// suffix range.
var docsRangeHeaders = []string{"bytes=0-9", "bytes=99999999-", "bytes=0-0,5-9", "bytes=-1"}

// docsRangePaths covers each documentation operation (getOpenApiSpec, getApiDocsAsset text and
// binary, getApiDocs, getContractSchema).
var docsRangePaths = []string{
	"/openapi.yaml",
	"/docs/swagger-ui.css",
	"/docs/favicon-32x32.png",
	"/docs",
	"/schemas/content.schema.json",
}

// identityEncoding asks for an unencoded body, so Content-Length (when sent) describes the body as
// transferred and the Go client does not negotiate gzip on its own.
var identityEncoding = map[string]string{"Accept-Encoding": "identity"}

func withHeader(h map[string]string, name, value string) map[string]string {
	out := maps.Clone(h)
	if out == nil {
		out = map[string]string{}
	}
	out[name] = value
	return out
}

// assertRangeIgnored checks a response to a Range request on a documentation path: 200 (never 206
// or 416), the full body, the same Content-Type as the plain GET (never multipart/byteranges), a
// Content-Length equal to the full size when one is sent, no Content-Range, and no Accept-Ranges
// that advertises byte ranges (the paths do not serve ranges, README §7).
func assertRangeIgnored(t *testing.T, what string, status int, hdr http.Header, body []byte, wantContentType string, want []byte) {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("%s status = %d, want 200 (README §7: no 206/416); body: %.200s", what, status, body)
	}
	ct := hdr.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(ct); err == nil && strings.HasPrefix(mt, "multipart/") {
		t.Fatalf("%s Content-Type = %q: a multipart range response", what, ct)
	}
	if ct != wantContentType {
		t.Errorf("%s Content-Type = %q, want %q (the plain GET's)", what, ct, wantContentType)
	}
	if !bytes.Equal(body, want) {
		t.Errorf("%s body (%d bytes) is not the full body (%d bytes)", what, len(body), len(want))
	}
	if cl := hdr.Values("Content-Length"); len(cl) > 0 {
		if len(cl) != 1 || strings.TrimSpace(cl[0]) != strconv.Itoa(len(want)) {
			t.Errorf("%s Content-Length = %q, want exactly one %d (the full size)", what, cl, len(want))
		}
	}
	if cr := hdr.Values("Content-Range"); len(cr) > 0 {
		t.Errorf("%s has Content-Range %q; a 200 full-body response must not", what, cr)
	}
	for _, v := range hdr.Values("Accept-Ranges") {
		if strings.Contains(strings.ToLower(v), "bytes") {
			t.Errorf("%s advertises Accept-Ranges %q although ranges are not served (README §7)", what, v)
		}
	}
}

// D36, docs/contracts/README.md §7 (Range) and §11
// A Range request on any documentation path answers 200 with the full body, as the plain GET does;
// /openapi.yaml stays byte-identical to the contract.
func TestE2E_DocsRangeRequestsGetTheFullBody(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	for _, p := range docsRangePaths {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			status, hdr, full := doJSON(t, http.MethodGet, api+p, nil, identityEncoding)
			if status != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200; body: %.200s", p, status, full)
			}
			if len(full) == 0 {
				t.Fatalf("GET %s body is empty", p)
			}
			if p == "/openapi.yaml" && !bytes.Equal(full, readContractFile(t, "openapi.yaml")) {
				t.Fatalf("GET /openapi.yaml is not byte-identical to docs/contracts/openapi.yaml")
			}
			wantCT := hdr.Get("Content-Type")
			for _, r := range docsRangeHeaders {
				t.Run("Range "+r, func(t *testing.T) {
					t.Parallel()
					st, h, b := doJSON(t, http.MethodGet, api+p, nil, withHeader(identityEncoding, "Range", r))
					assertRangeIgnored(t, "GET "+p+" (Range: "+r+")", st, h, b, wantCT, full)
				})
			}
		})
	}
}

// docsName labels a path-parameter value in subtest names.
func docsName(n string) string {
	if n == "" {
		return "(empty)"
	}
	return n
}

// D36, docs/contracts/README.md §11; go-interfaces.md §httpapi; openapi.yaml /docs/{asset} and
// /schemas/{schema}
// The names the API serves are exactly the spec enums: the enum equals this suite's name table (which
// carries the media types), every enum value answers 200, and names outside the enum (files of
// swagger-ui-dist that are not embedded, the page itself, path escapes, the empty name and case
// variants) answer 404 `not_found` in the README §5 error shape.
func TestE2E_DocsServedNamesEqualTheSpecEnums(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	spec, err := apiContract.Spec()
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	var assetNames []string
	for _, a := range docsAssets {
		assetNames = append(assetNames, a.name)
	}
	tests := []struct {
		template, param, prefix string
		table                   []string
		notFound                []string
	}{
		{
			template: "/docs/{asset}", param: "asset", prefix: "/docs/",
			table: assetNames,
			notFound: []string{
				"index.html",
				"swagger-ui-bundle.js.map",
				"swagger-ui.js",
				"..%2Fopenapi.yaml",
				"",
				"SWAGGER-UI.CSS",
				"Swagger-UI-Bundle.js",
				"swagger-initializer.JS",
				"Favicon-32x32.png",
				"license",
				"Notice",
				"SWAGGER-UI-BUNDLE.JS.LICENSE.TXT",
			},
		},
		{
			template: "/schemas/{schema}", param: "schema", prefix: "/schemas/",
			table: docsSchemas,
			notFound: []string{
				"",
				"CONTENT.SCHEMA.JSON",
				"Segment-Rule.schema.json",
				"..%2Fopenapi.yaml",
				"llm%2Fcontent.tool.json",
				"content.tool.json",
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
			want := slices.Sorted(slices.Values(tc.table))
			if diff := cmp.Diff(want, enum); diff != "" {
				t.Fatalf("{%s} enum of openapi.yaml vs the names this suite checks (-table +enum):\n%s", tc.param, diff)
			}
			for _, name := range enum {
				t.Run("serves "+name, func(t *testing.T) {
					t.Parallel()
					status, _, body := doJSON(t, http.MethodGet, api+tc.prefix+name, nil, nil)
					if status != http.StatusOK {
						t.Fatalf("GET %s%s status = %d, want 200; body: %.200s", tc.prefix, name, status, body)
					}
					if len(body) == 0 {
						t.Fatalf("GET %s%s body is empty", tc.prefix, name)
					}
				})
			}
			for _, name := range tc.notFound {
				t.Run("404 "+docsName(name), func(t *testing.T) {
					t.Parallel()
					status, hdr, body := doJSON(t, http.MethodGet, api+tc.prefix+name, nil, nil)
					assertError(t, status, hdr, body, http.StatusNotFound, "not_found", false)
				})
			}
		})
	}
}
