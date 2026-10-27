package httpapi

import (
	"embed"
	"net/http"
	"strconv"
)

// Interactive documentation (docs/API.md, DECISIONS.md D36): GET /docs is a Swagger UI page,
// GET /docs/{asset} its vendored files, GET /openapi.yaml the contract and GET /schemas/{schema}
// the two JSON Schema files the contract references by relative $ref. The contract bytes come
// from the OpenAPI embed (byte-identical to docs/contracts/ by `make check-contract-copies`), so
// there is no second copy that can drift.
//
// docs/ holds our page (index.html, swagger-initializer.js) and the unmodified swagger-ui-dist
// 5.34.0 files (Apache-2.0; LICENSE and NOTICE kept). To bump: `npm pack swagger-ui-dist@<v>`,
// copy swagger-ui-bundle.js, swagger-ui-bundle.js.LICENSE.txt, swagger-ui.css,
// favicon-32x32.png, LICENSE and NOTICE over docs/, then update the version here and in D36.
// Integrity of the 5.34.0 tarball (npm dist.integrity):
// sha512-H6tAIjr3XqE31PbkZl0OFya1ziRLDfTwh3J6OHdumPtPCLfjTnyLeagl/55kFNZcTOz9a9QI9WP0FXldwiGJag==
//
//go:embed docs
var docsFS embed.FS

// docsAssets is the closed set of names /docs/{asset} serves (the enum of the spec parameter) and
// their content types. Any other name is 404, so a request can never reach another embedded file.
var docsAssets = map[string]string{
	"swagger-ui-bundle.js":             "text/javascript; charset=utf-8",
	"swagger-ui-bundle.js.LICENSE.txt": "text/plain; charset=utf-8",
	"swagger-ui.css":                   "text/css; charset=utf-8",
	"swagger-initializer.js":           "text/javascript; charset=utf-8",
	"favicon-32x32.png":                "image/png",
	"LICENSE":                          "text/plain; charset=utf-8",
	"NOTICE":                           "text/plain; charset=utf-8",
}

// contractSchemas are the files /schemas/{schema} serves (the enum of the spec parameter).
var contractSchemas = map[string]bool{
	"segment-rule.schema.json": true,
	"content.schema.json":      true,
}

// docsCSP mirrors the CSP of web/nginx.conf: Swagger UI needs nothing looser (no inline script,
// no eval, no remote origin), and connect-src 'self' keeps "Try it out" on the page's own origin.
const docsCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// writeDocFile answers 200 with a static, read-only body and the security headers every
// documentation response carries.
func writeDocFile(w http.ResponseWriter, r *http.Request, contentType, cacheControl string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", cacheControl)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", docsCSP)
	// The whole body, always: the spec defines only 200 here, so a Range request must not turn
	// into 206/416/multipart (which http.ServeContent would do). HEAD gets the headers only.
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body) //nolint:gosec // embedded read-only files with a fixed content type, no request data
}

func (a *api) getAPIDocs(w http.ResponseWriter, r *http.Request) {
	body, err := docsFS.ReadFile("docs/index.html")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	writeDocFile(w, r, "text/html; charset=utf-8", "no-cache", body)
}

func (a *api) getAPIDocsAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("asset")
	contentType, ok := docsAssets[name]
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found", "route not found")
		return
	}
	body, err := docsFS.ReadFile("docs/" + name)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	writeDocFile(w, r, contentType, "public, max-age=3600", body)
}

func (a *api) getOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	body, err := OpenAPI.ReadFile("openapi/openapi.yaml")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	writeDocFile(w, r, "application/yaml", "no-cache", body)
}

func (a *api) getContractSchema(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("schema")
	if !contractSchemas[name] {
		writeError(w, r, http.StatusNotFound, "not_found", "route not found")
		return
	}
	body, err := OpenAPI.ReadFile("openapi/schemas/" + name)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	writeDocFile(w, r, "application/schema+json", "no-cache", body)
}
