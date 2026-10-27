package e2e_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// e2eCostBound is a generous wall-clock bound for one request over the limits: the linear
// Parse needs milliseconds; the quadratic behavior it replaced needed seconds for these inputs.
const e2eCostBound = 2 * time.Second

const e2eCond = `{"field":"city","op":"eq","value":"İzmir"}`

// REQ-AUD-01, REQ-AUD-02, REQ-AI-01
// POST /api/segments/count with a pathological body under the 64 KiB limit — 8000 nested "not"
// groups, or 1400 conditions in one group — is refused with 422 and the documented single field
// error (segment-dsl.md §7.3: depth at the path of the first group over MaxDepth, count at "rule"),
// quickly, and the service keeps answering.
func TestE2E_CountSegmentLimitsAreCheap(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)

	deep := `{"rule":` + strings.Repeat(`{"not":`, 8000) + e2eCond + strings.Repeat("}", 8000) + "}"
	flat := `{"rule":{"all":[` + strings.TrimSuffix(strings.Repeat(e2eCond+",", 1400), ",") + `]}}`
	tests := []struct {
		name string
		body string
		want fieldWant
	}{
		{name: "8000 nested not groups", body: deep, want: fieldWant{"rule.not.not.not", "max_depth_exceeded"}},
		{name: "1400 conditions", body: flat, want: fieldWant{"rule", "max_conditions_exceeded"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if len(tc.body) > 65536 {
				t.Fatalf("test setup: body is %d bytes, want <= 65536 so the size limit does not answer first", len(tc.body))
			}
			start := time.Now()
			status, hdr, body := doJSON(t, http.MethodPost, api+"/api/segments/count", []byte(tc.body), nil)
			elapsed := time.Since(start)
			e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
			got := make([]fieldWant, 0, len(e.Fields))
			for _, f := range e.Fields {
				got = append(got, fieldWant{f.Path, f.Code})
			}
			if diff := cmp.Diff([]fieldWant{tc.want}, got); diff != "" {
				t.Fatalf("error.fields mismatch (-want +got):\n%s", diff)
			}
			if elapsed > e2eCostBound {
				t.Fatalf("422 took %v, want < %v", elapsed, e2eCostBound)
			}
			assertExampleCount(t, api, m)
		})
	}
}

// REQ-AUD-02, REQ-UI-01
// Over HTTP, an unknown key that is not a short identifier never reaches error.fields[].path: it
// becomes "_invalid_key_" (segment-dsl.md §7.3); a short identifier is echoed. A body with more than
// 20 problems returns at most 20 field errors, the first ones in depth-first order.
func TestE2E_CountSegmentErrorPaths(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	rule := string(m.ExampleBrief.Rule)

	keys := []struct {
		name, body, path string
	}{
		{name: "identifier key is echoed", body: `{"rule":` + rule + `,"raw_sql":"x"}`, path: "raw_sql"},
		{name: "key with a space is replaced", body: `{"rule":` + rule + `,"drop table customers":1}`, path: "_invalid_key_"},
		{name: "key with HTML is replaced", body: `{"rule":` + rule + `,"<img src=x onerror=alert(1)>":1}`, path: "_invalid_key_"},
		{name: "key of 65 chars is replaced", body: `{"rule":` + rule + `,"` + strings.Repeat("k", 65) + `":1}`, path: "_invalid_key_"},
		{name: "rule-level key with a quote is replaced", body: `{"rule":{"a\"b":1}}`, path: "rule._invalid_key_"},
	}
	for _, tc := range keys {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, http.MethodPost, api+"/api/segments/count", []byte(tc.body), nil)
			e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
			if !e.has(fieldWant{tc.path, "schema_violation"}) {
				t.Fatalf("error.fields = %+v, want {%s schema_violation}", e.Fields, tc.path)
			}
			for _, f := range e.Fields {
				if strings.ContainsAny(f.Path, " <>\"") || strings.Contains(f.Path, strings.Repeat("k", 65)) {
					t.Fatalf("error path %q carries raw key text", f.Path)
				}
			}
		})
	}

	t.Run("30 empty groups return the first 20 errors", func(t *testing.T) {
		t.Parallel()
		body := `{"rule":{"all":[` + strings.TrimSuffix(strings.Repeat(`{"all":[]},`, 30), ",") + `]}}`
		status, hdr, resp := doJSON(t, http.MethodPost, api+"/api/segments/count", []byte(body), nil)
		e := assertError(t, status, hdr, resp, http.StatusUnprocessableEntity, "validation_failed", true)
		want := make([]fieldWant, 20)
		for i := range want {
			want[i] = fieldWant{fmt.Sprintf("rule.all[%d].all", i), "empty_group"}
		}
		got := make([]fieldWant, 0, len(e.Fields))
		for _, f := range e.Fields {
			got = append(got, fieldWant{f.Path, f.Code})
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("error.fields mismatch (-want +got):\n%s", diff)
		}
	})
}
