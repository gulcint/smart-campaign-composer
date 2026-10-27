package contract_test

// Interactive API documentation in the contract (DECISIONS.md D36; docs/contracts/README.md §1 and §11;
// docs/contracts/go-interfaces.md §httpapi "Doküman rotaları (D36)"). There is no REQ id for this
// feature; the tests trace to D36 and README §11. Route ↔ spec equality as a whole is already
// TestRoutes_MatchOpenAPIPaths; here only the D36-specific facts are checked.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"

	"github.com/gulcint/smart-campaign-composer/internal/httpapi"
	"github.com/gulcint/smart-campaign-composer/test/contract"
)

// docsOps are the four `documentation` operations of D36 (method, path template, operationId).
var docsOps = []apiOp{
	{Method: "GET", Template: "/docs", ID: "getApiDocs"},
	{Method: "GET", Template: "/docs/{asset}", ID: "getApiDocsAsset"},
	{Method: "GET", Template: "/openapi.yaml", ID: "getOpenApiSpec"},
	{Method: "GET", Template: "/schemas/{schema}", ID: "getContractSchema"},
}

type apiOp struct{ Method, Template, ID string }

// specRelativeRefRE matches a relative file $ref (`$ref: './schemas/x.schema.json#/$defs/y'`, quotes and
// the leading `./` optional, fragment optional) and captures only the file name. Anchored on `$ref:` so
// prose mentioning schemas/... does not count.
var specRelativeRefRE = regexp.MustCompile(`\$ref:\s*['"]?(?:\./)?schemas/([^'"#\s,}/]+)`)

// loadModel builds the libopenapi v3 model of docs/contracts/openapi.yaml (same loader settings as
// apispec.Load, which loadSpec has already validated).
func loadModel(t *testing.T) (*v3.Document, []byte) {
	t.Helper()
	path := loadSpec(t).Path()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc, err := libopenapi.NewDocumentWithConfiguration(raw, &datamodel.DocumentConfiguration{
		BasePath:            filepath.Dir(path),
		AllowFileReferences: true,
	})
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	built, _ := doc.BuildV3Model()
	if built == nil {
		t.Fatalf("build OpenAPI 3 model of %s", path)
	}
	return &built.Model, raw
}

func operationsOf(p *v3.PathItem) map[string]*v3.Operation {
	out := map[string]*v3.Operation{}
	for m, op := range map[string]*v3.Operation{
		"GET": p.Get, "PUT": p.Put, "POST": p.Post, "DELETE": p.Delete,
		"PATCH": p.Patch, "OPTIONS": p.Options, "HEAD": p.Head, "TRACE": p.Trace,
	} {
		if op != nil {
			out[m] = op
		}
	}
	return out
}

// pathEnum returns the enum values of the path parameter name declared on the path item or its GET.
func pathEnum(t *testing.T, p *v3.PathItem, name string) []string {
	t.Helper()
	params := slices.Clone(p.Parameters)
	if p.Get != nil {
		params = append(params, p.Get.Parameters...)
	}
	for _, prm := range params {
		if prm == nil || prm.Name != name || prm.In != "path" || prm.Schema == nil {
			continue
		}
		s := prm.Schema.Schema()
		if s == nil {
			t.Fatalf("path parameter %q has no resolvable schema", name)
		}
		var out []string
		for _, n := range s.Enum {
			out = append(out, n.Value)
		}
		slices.Sort(out)
		return out
	}
	t.Fatalf("path parameter %q not found", name)
	return nil
}

// D36, docs/contracts/README.md §11
// httpapi.Routes() lists the four documentation routes (go-interfaces.md §httpapi), the spec defines
// them under the same operationIds with the `documentation` tag, and they are not in the phase
// allowlist (they are implemented; README §11: no separate exception for api-routes).
func TestRoutes_ListTheDocumentationOperations(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	routes := httpapi.Routes()
	specIDs := map[string]string{}
	for _, op := range spec.Operations() {
		specIDs[key(op.Method, op.Template)] = op.ID
	}
	for _, want := range docsOps {
		t.Run(want.ID, func(t *testing.T) {
			t.Parallel()
			if !slices.Contains(routes, httpapi.Route{Method: want.Method, Template: want.Template}) {
				t.Errorf("httpapi.Routes() lacks %s", key(want.Method, want.Template))
			}
			if got := specIDs[key(want.Method, want.Template)]; got != want.ID {
				t.Errorf("openapi.yaml operationId of %s = %q, want %q", key(want.Method, want.Template), got, want.ID)
			}
			if phase, listed := contract.NotImplemented[want.ID]; listed {
				t.Errorf("%s is in test/contract/allowlist.go (%s) but D36 implements it", want.ID, phase)
			}
		})
	}
}

// D36, docs/contracts/README.md §1 and §11
// Spec-level facts of D36: `servers` is the single relative "/", the spec has 23 operations
// (19 application + 4 `documentation`), exactly the four D36 operations carry the `documentation`
// tag and it is a declared tag.
func TestOpenAPI_DocumentationFacts(t *testing.T) {
	t.Parallel()
	model, _ := loadModel(t)

	t.Run("servers is the relative /", func(t *testing.T) {
		t.Parallel()
		var got []string
		for _, s := range model.Servers {
			got = append(got, s.URL)
		}
		if diff := cmp.Diff([]string{"/"}, got); diff != "" {
			t.Errorf("servers mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("23 operations", func(t *testing.T) {
		t.Parallel()
		if n := len(loadSpec(t).Operations()); n != 23 {
			t.Errorf("openapi.yaml has %d operations, want 23 (README §1: 19 application + 4 documentation)", n)
		}
	})

	t.Run("documentation tag is declared", func(t *testing.T) {
		t.Parallel()
		for _, tag := range model.Tags {
			if tag != nil && tag.Name == "documentation" {
				return
			}
		}
		t.Error("openapi.yaml tags do not declare `documentation`")
	})

	t.Run("exactly the four D36 operations are tagged documentation", func(t *testing.T) {
		t.Parallel()
		var got []apiOp
		for tpl, item := range model.Paths.PathItems.FromOldest() {
			for m, op := range operationsOf(item) {
				if slices.Contains(op.Tags, "documentation") {
					got = append(got, apiOp{Method: m, Template: tpl, ID: op.OperationId})
				}
			}
		}
		less := func(a, b apiOp) int {
			if c := strings.Compare(a.Template, b.Template); c != 0 {
				return c
			}
			return strings.Compare(a.Method, b.Method)
		}
		slices.SortFunc(got, less)
		want := slices.Clone(docsOps)
		slices.SortFunc(want, less)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("documentation operations mismatch (-want +got):\n%s", diff)
		}
	})
}

// D36 (deviation 2), docs/contracts/README.md §11
// The {schema} enum of GET /schemas/{schema} is exactly the set of schema files openapi.yaml references
// with relative $refs, and each of them exists under docs/contracts/schemas/ — otherwise a $ref shown
// in Swagger UI would resolve to a 404.
func TestOpenAPI_SchemaEnumMatchesRelativeRefs(t *testing.T) {
	t.Parallel()
	model, raw := loadModel(t)
	item, ok := model.Paths.PathItems.Get("/schemas/{schema}")
	if !ok || item == nil {
		t.Fatal("openapi.yaml has no /schemas/{schema} path")
	}
	enum := pathEnum(t, item, "schema")

	refSet := map[string]bool{}
	for _, m := range specRelativeRefRE.FindAllSubmatch(raw, -1) {
		refSet[string(m[1])] = true
	}
	var refs []string
	for r := range refSet {
		refs = append(refs, r)
	}
	slices.Sort(refs)
	if diff := cmp.Diff(refs, enum); diff != "" {
		t.Errorf("{schema} enum vs relative $ref targets of openapi.yaml (-refs +enum):\n%s", diff)
	}
	dir := filepath.Join(filepath.Dir(loadSpec(t).Path()), "schemas")
	for _, name := range enum {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("{schema} enum names %s, but docs/contracts/schemas/%s is missing: %v", name, name, err)
		}
	}
}
