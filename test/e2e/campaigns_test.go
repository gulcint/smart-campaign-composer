package e2e_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// REQ-AUD-00
// GET /api/campaigns (operationId listCampaigns): a CampaignPage, newest first, with the default
// limit 50 / offset 0, the requested limit/offset echoed, and limit/offset outside their ranges
// refused with 422 invalid_value on query.<name> (openapi.yaml components.parameters Limit/Offset).
// Other tests create campaigns in parallel, so only bounds that hold under concurrency are asserted.
func TestE2E_ListCampaigns(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	createCampaign(t, api, m.ExampleBrief.Brief)

	type page struct {
		Items  []map[string]any `json:"items"`
		Total  int              `json:"total"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	list := func(t *testing.T, query string) page {
		t.Helper()
		status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns"+query, nil, nil)
		if status != http.StatusOK {
			t.Fatalf("GET /api/campaigns%s status = %d, want 200; body: %s", query, status, body)
		}
		if diff := cmp.Diff([]string{"items", "limit", "offset", "total"}, sortedKeys(decodeObject(t, body))); diff != "" {
			t.Fatalf("CampaignPage keys mismatch (-want +got):\n%s", diff)
		}
		var p page
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatalf("decode CampaignPage: %v", err)
		}
		return p
	}

	pages := []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
	}{
		{name: "defaults are limit 50 offset 0", query: "", wantLimit: 50, wantOffset: 0},
		{name: "limit 1 is accepted", query: "?limit=1", wantLimit: 1, wantOffset: 0},
		{name: "limit 200 is accepted", query: "?limit=200&offset=0", wantLimit: 200, wantOffset: 0},
		{name: "offset is echoed", query: "?limit=2&offset=1", wantLimit: 2, wantOffset: 1},
	}
	for _, tc := range pages {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := list(t, tc.query)
			if p.Limit != tc.wantLimit || p.Offset != tc.wantOffset {
				t.Fatalf("limit/offset = %d/%d, want %d/%d", p.Limit, p.Offset, tc.wantLimit, tc.wantOffset)
			}
			if p.Total < 1 {
				t.Fatalf("total = %d, want >= 1 (this test created a campaign)", p.Total)
			}
			if len(p.Items) > p.Limit {
				t.Fatalf("%d items for limit %d", len(p.Items), p.Limit)
			}
			if tc.wantOffset == 0 && len(p.Items) == 0 {
				t.Fatalf("first page is empty although total = %d", p.Total)
			}
			var prev time.Time
			for i, c := range p.Items {
				if diff := cmp.Diff(campaignKeys, sortedKeys(c)); diff != "" {
					t.Fatalf("items[%d] Campaign keys mismatch (-want +got):\n%s", i, diff)
				}
				s, _ := c["created_at"].(string)
				ts, err := time.Parse(time.RFC3339Nano, s)
				if err != nil {
					t.Fatalf("items[%d].created_at = %q: %v", i, s, err)
				}
				if i > 0 && ts.After(prev) {
					t.Fatalf("items[%d].created_at %s is newer than items[%d] %s; want newest first", i, ts, i-1, prev)
				}
				prev = ts
			}
		})
	}

	rejected := []struct {
		name  string
		query string
		path  string
	}{
		{name: "limit 0", query: "?limit=0", path: "query.limit"},
		{name: "limit 201", query: "?limit=201", path: "query.limit"},
		{name: "limit not a number", query: "?limit=ten", path: "query.limit"},
		{name: "negative offset", query: "?offset=-1", path: "query.offset"},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, http.MethodGet, api+"/api/campaigns"+tc.query, nil, nil)
			e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
			if !e.has(fieldWant{tc.path, "invalid_value"}) {
				t.Fatalf("error.fields = %+v, want {%s invalid_value}", e.Fields, tc.path)
			}
		})
	}
}
