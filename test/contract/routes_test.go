package contract_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/httpapi"
	"github.com/gulcint/smart-campaign-composer/test/contract"
	"github.com/gulcint/smart-campaign-composer/test/testenv/apispec"
)

// loadSpec reads docs/contracts/openapi.yaml from disk (OPENAPI_SPEC_PATH or the module root).
func loadSpec(t *testing.T) *apispec.Spec {
	t.Helper()
	p, err := apispec.SpecPath()
	if err != nil {
		t.Fatalf("locate openapi.yaml: %v", err)
	}
	s, err := apispec.Load(p)
	if err != nil {
		t.Fatalf("load %s: %v", p, err)
	}
	return s
}

func key(method, template string) string { return method + " " + template }

// templateRE: absolute path, segments are literals or OpenAPI {name} parameters (no ":name", "*").
var templateRE = regexp.MustCompile(`^(/([A-Za-z0-9._-]+|\{[a-z_][a-z0-9_]*\}))+$`)

// REQ-AUD-00, REQ-TRK-03, REQ-AI-01
// Gate `api-routes` (docs/contracts/README.md §11, go-interfaces.md §httpapi): httpapi.Routes() and
// the paths × methods of openapi.yaml are equal in both directions — no undocumented endpoint, no
// documented endpoint without a route — except the operations of the phase allowlist, which must
// NOT have a route yet. SSE operations are included.
func TestRoutes_MatchOpenAPIPaths(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	routes := httpapi.Routes()

	specOps := map[string]apispec.Operation{}
	for _, op := range spec.Operations() {
		specOps[key(op.Method, op.Template)] = op
	}
	routeSet := map[string]bool{}
	for _, r := range routes {
		routeSet[key(r.Method, r.Template)] = true
	}

	t.Run("Routes is not empty", func(t *testing.T) {
		if len(routes) == 0 {
			t.Fatal("httpapi.Routes() returned no routes")
		}
	})

	t.Run("routes use upper-case methods and OpenAPI {name} templates", func(t *testing.T) {
		for _, r := range routes {
			if r.Method == "" || r.Method != strings.ToUpper(r.Method) {
				t.Errorf("route %q: method must be upper case", key(r.Method, r.Template))
			}
			if !templateRE.MatchString(r.Template) {
				t.Errorf("route %q: template is not an OpenAPI path template", key(r.Method, r.Template))
			}
		}
	})

	t.Run("no route is listed twice", func(t *testing.T) {
		seen := map[string]int{}
		for _, r := range routes {
			seen[key(r.Method, r.Template)]++
		}
		for k, n := range seen {
			if n > 1 {
				t.Errorf("route %q is listed %d times", k, n)
			}
		}
	})

	t.Run("routes are sorted by template then method", func(t *testing.T) {
		sorted := slices.IsSortedFunc(routes, func(a, b httpapi.Route) int {
			if c := strings.Compare(a.Template, b.Template); c != 0 {
				return c
			}
			return strings.Compare(a.Method, b.Method)
		})
		if !sorted {
			t.Errorf("httpapi.Routes() is not sorted by Template, then Method:\n%s", formatRoutes(routes))
		}
	})

	t.Run("every route is a documented operation", func(t *testing.T) {
		for _, r := range routes {
			if _, ok := specOps[key(r.Method, r.Template)]; !ok {
				t.Errorf("route %q is not in openapi.yaml (undocumented endpoint): add it to the spec in the same PR or remove the route", key(r.Method, r.Template))
			}
		}
	})

	t.Run("every documented operation outside the allowlist has a route", func(t *testing.T) {
		for k, op := range specOps {
			if _, allowed := contract.NotImplemented[op.ID]; allowed {
				continue
			}
			if !routeSet[k] {
				t.Errorf("operationId %s (%s) has no route in httpapi.Routes()", op.ID, k)
			}
		}
	})

	t.Run("allowlisted operations have no route yet", func(t *testing.T) {
		for k, op := range specOps {
			phase, allowed := contract.NotImplemented[op.ID]
			if allowed && routeSet[k] {
				t.Errorf("operationId %s (%s) is allowlisted for %s but has a route: delete it from test/contract/allowlist.go", op.ID, k, phase)
			}
		}
	})

	t.Run("allowlist names only documented operations", func(t *testing.T) {
		if unknown := spec.UnknownIDs(contract.NotImplemented); len(unknown) > 0 {
			t.Errorf("allowlist names operationIds that are not in openapi.yaml: %v", unknown)
		}
	})

	t.Run("both SSE operations are known to the spec", func(t *testing.T) {
		var got []string
		for _, op := range spec.Operations() {
			if op.SSE {
				got = append(got, op.ID)
			}
		}
		slices.Sort(got)
		if diff := cmp.Diff([]string{"generateContent", "streamCampaignEvents"}, got); diff != "" {
			t.Errorf("operations with x-sse-events mismatch (-want +got):\n%s", diff)
		}
	})
}

// REQ-AUD-00
// Routes is a pure function of the code: two calls return the same table.
func TestRoutes_IsDeterministic(t *testing.T) {
	t.Parallel()
	if diff := cmp.Diff(httpapi.Routes(), httpapi.Routes()); diff != "" {
		t.Fatalf("httpapi.Routes() differs between calls (-first +second):\n%s", diff)
	}
}

func formatRoutes(rs []httpapi.Route) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "  %s %s\n", r.Method, r.Template)
	}
	return b.String()
}
