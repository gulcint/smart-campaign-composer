package campaign_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// matchedCustomerID returns one customer matched by the example brief rule (manifest group
// long_first_name, matches_example_brief = true).
func matchedCustomerID(t *testing.T) string {
	t.Helper()
	g := readGroupsForCampaign(t)
	ids := g.Groups["long_first_name"].CustomerIDs
	if len(ids) == 0 {
		t.Fatal("manifest group long_first_name is empty")
	}
	return ids[0]
}

type campaignGroups struct {
	Groups map[string]struct {
		CustomerIDs []string `json:"customer_ids"`
	} `json:"groups"`
}

func readGroupsForCampaign(t *testing.T) campaignGroups {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(svcManifestPath))
	if err != nil {
		t.Fatalf("read %s: %v", svcManifestPath, err)
	}
	var g campaignGroups
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("decode manifest groups: %v", err)
	}
	return g
}

// setCustomerColumn updates one text column of one customer through the scc_app pool and restores
// the original value when the test ends.
func (h *harness) setCustomerColumn(t *testing.T, id, column, value string) {
	t.Helper()
	if column != "first_name" && column != "last_name" {
		t.Fatalf("setCustomerColumn: unsupported column %q", column)
	}
	var orig string
	if err := h.pool.QueryRow(t.Context(), `SELECT `+column+` FROM customers WHERE id = $1`, id).Scan(&orig); err != nil {
		t.Fatalf("read customers.%s: %v", column, err)
	}
	t.Cleanup(func() {
		if _, err := h.pool.Exec(context.Background(), `UPDATE customers SET `+column+` = $1 WHERE id = $2`, orig, id); err != nil {
			t.Errorf("restore customers.%s of %s: %v", column, id, err)
		}
	})
	tag, err := h.pool.Exec(t.Context(), `UPDATE customers SET `+column+` = $1 WHERE id = $2`, value, id)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("UPDATE customers.%s: rows %d, err %v", column, tag.RowsAffected(), err)
	}
}

// REQ-CNT-05, REQ-APR-03 (openapi approveContent check 6, D37)
// The approval re-checks the worst case with the longest values of the customers matched by the
// rule NOW (before consent): content validated at the 160/80 boundary, then a matched customer gets
// a longer first_name (or last_name) => Approve is a *validation.Error with
// sms_too_long_worst_case (or subject_too_long_worst_case), limit and actual set; nothing changes.
//
// Runs on the integration database owned by this test run (TEST_DATABASE_URL), never on the shared
// E2E database. It is deliberately NOT parallel: Go starts this package's parallel tests only after
// the sequential ones finished, so no other campaign test sees the mutated customer; the original
// value is restored in t.Cleanup.
func TestService_ApproveRechecksWorstCaseWithCurrentCustomers(t *testing.T) {
	cases := []struct {
		name    string
		column  string
		prepare func(t *testing.T, h *harness, id string, l content.Longest) string // returns the version to approve
		value   func(l content.Longest) string
		path    string
		code    string
		limit   int
		actual  int
		match   []error
	}{
		{
			name:   "longer first name breaks the SMS",
			column: "first_name",
			prepare: func(t *testing.T, h *harness, id string, l content.Longest) string {
				v := h.mustValidated(t, scen(llm.ScenarioOK), id) // CNT_OK: exactly 160 with the longest first name
				if v.WorstCase.SMSRunes != 160 {
					t.Fatalf("setup: CNT_OK worst case = %d, want 160 at the boundary", v.WorstCase.SMSRunes)
				}
				return v.ContentVersionID
			},
			value:  func(l content.Longest) string { return strings.Repeat("Ş", l.FirstName+1) },
			path:   "sms.body",
			code:   validation.CodeSMSTooLongWorstCase,
			limit:  160,
			actual: 161,
			match:  []error{content.ErrSMSTooLong, content.ErrWorstCaseTooLong},
		},
		{
			name:   "longer last name breaks the subject",
			column: "last_name",
			prepare: func(t *testing.T, h *harness, id string, l content.Longest) string {
				c := okContent()
				c.Email.Subject = "{{last_name}}" + strings.Repeat("b", 80-l.LastName)
				v := h.edit(t, id, campaign.ExpectedVersionNone, c)
				if v.WorstCase == nil || v.WorstCase.SubjectRunes != 80 {
					t.Fatalf("setup: subject worst case = %v, want 80 at the boundary", v.WorstCase)
				}
				return v.ID
			},
			value:  func(l content.Longest) string { return strings.Repeat("Ğ", l.LastName+1) },
			path:   "email.subject",
			code:   validation.CodeSubjectTooLongWorstCase,
			limit:  80,
			actual: 81,
			match:  []error{content.ErrSubjectTooLong, content.ErrWorstCaseTooLong},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMockHarness(t)
			id := h.audienceReady(t)
			l := h.get(t, id).Segment.LongestValues
			vid := tc.prepare(t, h, id, l)

			h.setCustomerColumn(t, matchedCustomerID(t), tc.column, tc.value(l))

			_, err := h.svc.Approve(context.Background(), id, vid)
			var ve *validation.Error
			if !errors.As(err, &ve) {
				t.Fatalf("Approve() error = %v, want a *validation.Error (HTTP 422 validation_failed)", err)
			}
			for _, s := range tc.match {
				if !errors.Is(err, s) {
					t.Fatalf("Approve() error = %v, want it to match %v", err, s)
				}
			}
			found := false
			for _, f := range ve.Fields {
				if f.Path == tc.path && f.Code == tc.code {
					found = true
					if f.Limit == nil || *f.Limit != tc.limit || f.Actual == nil || *f.Actual != tc.actual {
						t.Fatalf("%s limit/actual = %v/%v, want %d/%d", f.Code, ptrI(f.Limit), ptrI(f.Actual), tc.limit, tc.actual)
					}
				}
			}
			if !found {
				t.Fatalf("field errors = %+v, want %s %s", ve.Fields, tc.path, tc.code)
			}

			c := h.get(t, id)
			if c.State != campaign.StateContentReady || c.Approval != nil {
				t.Fatalf("after the refused approval state/approval = %s/%+v, want content_ready/nil", c.State, c.Approval)
			}
			if rows := h.approvalRows(t, id); len(rows) != 0 {
				t.Fatalf("approvals rows = %d, want 0", len(rows))
			}
		})
	}
}
