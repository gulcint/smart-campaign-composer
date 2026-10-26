package seed_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/seed"
)

// REQ-SEED-01
func TestGenerate_RejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		opts    seed.Options
		wantErr error
	}{
		{name: "199 users is too few", opts: seed.Options{Seed: 42, Users: 199, RefTime: canonicalRefTime}, wantErr: seed.ErrTooFewUsers},
		{name: "zero users is too few", opts: seed.Options{Seed: 42, Users: 0, RefTime: canonicalRefTime}, wantErr: seed.ErrTooFewUsers},
		{name: "negative users is too few", opts: seed.Options{Seed: 42, Users: -1, RefTime: canonicalRefTime}, wantErr: seed.ErrTooFewUsers},
		{name: "zero ref time is invalid", opts: seed.Options{Seed: 42, Users: 5000}, wantErr: seed.ErrInvalidRefTime},
		{name: "200 users (MinUsers) is accepted", opts: seed.Options{Seed: 42, Users: seed.MinUsers, RefTime: canonicalRefTime}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := seed.Generate(tc.opts)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Generate(%+v) error = %v, want %v", tc.opts, err, tc.wantErr)
			}
			if tc.wantErr == nil && d == nil {
				t.Fatal("Generate() returned a nil dataset without error")
			}
		})
	}
	if seed.MinUsers != 200 {
		t.Fatalf("MinUsers = %d, want 200", seed.MinUsers)
	}
}

// REQ-SEED-01
// Same (--seed, --users, --ref-time) => byte-identical export and manifest; other parameters differ.
func TestGenerate_IsByteDeterministic(t *testing.T) {
	t.Parallel()
	a := canonical(t)
	b := generate(t, a.opts)
	if !bytes.Equal(a.jsonl, b.jsonl) {
		t.Fatal("two runs with the same options produced different exports")
	}
	if !bytes.Equal(a.manifest, b.manifest) {
		t.Fatal("two runs with the same options produced different manifests")
	}

	variants := []struct {
		name string
		opts seed.Options
	}{
		{name: "different seed changes the data", opts: seed.Options{Seed: 43, Users: canonicalUsers, RefTime: canonicalRefTime}},
		{name: "different ref time changes the data", opts: seed.Options{Seed: canonicalSeed, Users: canonicalUsers, RefTime: canonicalRefTime.Add(24 * time.Hour)}},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			other := generate(t, v.opts)
			if bytes.Equal(a.jsonl, other.jsonl) {
				t.Fatalf("export for %+v equals the canonical export", v.opts)
			}
		})
	}
}

// REQ-SEED-01
// testdata/seed/manifest.json is exactly the canonical Generate output (seed.md §2, README §7).
func TestManifest_MatchesCommittedFile(t *testing.T) {
	t.Parallel()
	g := canonical(t)
	committed := readCommittedManifest(t)
	if !bytes.Equal(committed, g.manifest) {
		t.Fatalf("%s differs from seed.Generate(seed=42, users=5000, ref=2026-01-15T12:00:00Z).Manifest().MarshalCanonical(); regenerate it with cmd/seed --skip-db", manifestPath)
	}
}

// REQ-SEED-01
// Canonical form: 2-space indent, keys in seed.md §5 order, exactly one trailing newline.
func TestManifest_CanonicalFormat(t *testing.T) {
	t.Parallel()
	m := canonical(t).manifest
	if !bytes.HasPrefix(m, []byte("{\n  \"schema_version\": 1,\n")) {
		t.Fatalf("manifest does not start with a 2-space indented schema_version: %q", m[:min(len(m), 40)])
	}
	if !bytes.HasSuffix(m, []byte("}\n")) || bytes.HasSuffix(m, []byte("\n\n")) {
		t.Fatal("manifest must end with exactly one trailing newline")
	}
	keys, err := objectKeys(m)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if diff := cmp.Diff(manifestTopKeys, keys); diff != "" {
		t.Fatalf("top-level key order (-want +got):\n%s", diff)
	}
	gkeys, err := nestedObjectKeys(m, "groups")
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
	if diff := cmp.Diff(groupNames, gkeys); diff != "" {
		t.Fatalf("groups key order (-want +got):\n%s", diff)
	}
}

// REQ-SEED-01
func TestManifest_ParamsAndDatasetHash(t *testing.T) {
	t.Parallel()
	g := canonical(t)
	if g.doc.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", g.doc.SchemaVersion)
	}
	if g.doc.Params.Seed != canonicalSeed || g.doc.Params.Users != canonicalUsers {
		t.Fatalf("params = %+v, want seed %d users %d", g.doc.Params, canonicalSeed, canonicalUsers)
	}
	ref, err := time.Parse(time.RFC3339, g.doc.Params.RefTime)
	if err != nil || !ref.Equal(canonicalRefTime) {
		t.Fatalf("params.ref_time = %q, want %s", g.doc.Params.RefTime, canonicalRefTime.Format(time.RFC3339))
	}
	sum := sha256.Sum256(g.jsonl)
	if want := hex.EncodeToString(sum[:]); g.doc.DatasetSHA256 != want {
		t.Fatalf("dataset_sha256 = %q, want sha256(export) = %q", g.doc.DatasetSHA256, want)
	}
}

// REQ-SEED-01
// Customers then events, each sorted by id; customer ids are UUIDv5(NS, "scc-seed/<seed>/customer/<index>").
func TestExport_OrderAndDeterministicIDs(t *testing.T) {
	t.Parallel()
	g := canonical(t)
	if len(g.customers) != canonicalUsers {
		t.Fatalf("customers = %d, want --users = %d", len(g.customers), canonicalUsers)
	}
	if !slices.IsSortedFunc(g.customers, func(a, b customer) int { return strings.Compare(a.ID, b.ID) }) {
		t.Fatal("customers are not sorted by id")
	}
	if !slices.IsSortedFunc(g.events, func(a, b event) int { return strings.Compare(a.ID, b.ID) }) {
		t.Fatal("events are not sorted by id")
	}

	want := map[string]bool{}
	for i := range canonicalUsers {
		want[uuidV5(t, seedNamespace, "scc-seed/"+strconv.FormatInt(canonicalSeed, 10)+"/customer/"+strconv.Itoa(i))] = true
	}
	ids := map[string]bool{}
	for _, c := range g.customers {
		if !want[c.ID] {
			t.Fatalf("customer id %s is not UUIDv5(NS, scc-seed/42/customer/<index>)", c.ID)
		}
		ids[c.ID] = true
	}
	if len(ids) != canonicalUsers {
		t.Fatalf("distinct customer ids = %d, want %d", len(ids), canonicalUsers)
	}
	evIDs := map[string]bool{}
	for _, e := range g.events {
		if !ids[e.CustomerID] {
			t.Fatalf("event %s references unknown customer %s", e.ID, e.CustomerID)
		}
		if evIDs[e.ID] {
			t.Fatalf("duplicate event id %s", e.ID)
		}
		evIDs[e.ID] = true
	}
}

var amountFormat = regexp.MustCompile(`^[0-9]+\.[0-9]{2}$`)

// REQ-SEED-01
// The data carries name, email, phone, city, consent and shopping history (cart_add / purchase
// events with dates); no event lies after the reference time.
func TestDataset_ShoppingHistoryAndConsent(t *testing.T) {
	t.Parallel()
	g := canonical(t)
	ref := canonicalRefTime
	byType := map[string]int{}
	for _, e := range g.events {
		byType[e.Type]++
		if e.at.After(ref) {
			t.Fatalf("event %s occurs at %s, after ref %s", e.ID, e.OccurredAt, ref.Format(time.RFC3339))
		}
		switch e.Type {
		case "purchase":
			if e.Amount == nil {
				t.Fatalf("purchase %s has no amount", e.ID)
			}
			if !amountFormat.MatchString(e.Amount.String()) {
				t.Fatalf("purchase %s amount %q is not a number with 2 decimals", e.ID, e.Amount.String())
			}
			if f, _ := e.Amount.Float64(); f <= 0 {
				t.Fatalf("purchase %s amount %s must be > 0", e.ID, e.Amount.String())
			}
		case "cart_add", "page_view":
			if e.Amount != nil {
				t.Fatalf("%s event %s has amount %s, want null", e.Type, e.ID, e.Amount.String())
			}
		default:
			t.Fatalf("event %s has type %q, want cart_add|purchase|page_view", e.ID, e.Type)
		}
	}
	for _, typ := range []string{"cart_add", "purchase", "page_view"} {
		if byType[typ] == 0 {
			t.Fatalf("no %s events in the dataset", typ)
		}
	}

	var sms, email, both, none int
	for _, c := range g.customers {
		if c.City == "" {
			t.Fatalf("customer %s has empty city", c.ID)
		}
		if !slices.Contains([]string{"tr", "en"}, c.Language) {
			t.Fatalf("customer %s language %q", c.ID, c.Language)
		}
		if !slices.Contains([]string{"bronze", "silver", "gold", "platinum"}, c.LoyaltyTier) {
			t.Fatalf("customer %s loyalty_tier %q", c.ID, c.LoyaltyTier)
		}
		if !c.SMSOptIn && c.SMSOptInAt != nil {
			t.Fatalf("customer %s: sms_opt_in=false but sms_opt_in_at is set", c.ID)
		}
		if !c.EmailOptIn && c.EmailOptInAt != nil {
			t.Fatalf("customer %s: email_opt_in=false but email_opt_in_at is set", c.ID)
		}
		switch {
		case c.SMSOptIn && c.EmailOptIn:
			both++
		case c.SMSOptIn:
			sms++
		case c.EmailOptIn:
			email++
		default:
			none++
		}
	}
	if both == 0 || sms == 0 || email == 0 || none == 0 {
		t.Fatalf("consent combinations both/sms_only/email_only/none = %d/%d/%d/%d; every combination must occur", both, sms, email, none)
	}
}

// REQ-SEED-01
// Manifest totals, consent, contact, reachable and cities equal what the export contains.
func TestManifest_TotalsMatchExport(t *testing.T) {
	t.Parallel()
	g := canonical(t)
	d := g.doc

	byType := map[string]int{}
	for _, e := range g.events {
		byType[e.Type]++
	}
	var consent consentDoc
	var reachable audienceDoc
	var missingPhone, missingEmail int
	cities := map[string]int{}
	for _, c := range g.customers {
		addConsent(&consent, c)
		reachable.Matched++
		if c.SMSOptIn && c.Phone != nil {
			reachable.SMS++
		}
		if c.EmailOptIn && c.Email != nil {
			reachable.Email++
		}
		if c.Phone == nil {
			missingPhone++
		}
		if c.Email == nil {
			missingEmail++
		}
		cities[c.City]++
	}

	checks := []struct {
		name      string
		got, want any
	}{
		{"totals.customers", d.Totals.Customers, len(g.customers)},
		{"totals.events", d.Totals.Events, len(g.events)},
		{"totals.events_by_type.cart_add", d.Totals.EventsByType.CartAdd, byType["cart_add"]},
		{"totals.events_by_type.purchase", d.Totals.EventsByType.Purchase, byType["purchase"]},
		{"totals.events_by_type.page_view", d.Totals.EventsByType.PageView, byType["page_view"]},
		{"consent", d.Consent, consent},
		{"contact.missing_phone", d.Contact.MissingPhone, missingPhone},
		{"contact.missing_email", d.Contact.MissingEmail, missingEmail},
		{"reachable", d.Reachable, reachable},
		{"cities", d.Cities, cities},
	}
	for _, c := range checks {
		if diff := cmp.Diff(c.want, c.got); diff != "" {
			t.Errorf("%s mismatch (-export +manifest):\n%s", c.name, diff)
		}
	}
}

// REQ-SEED-01
// example_brief: the brief, the segment-dsl §7.1 rule, and audience / consent / longest values
// recomputed from the export (matched = İstanbul AND cart_add in [ref-30d, ref] AND no purchase in
// that window; sms/email = matched AND opt-in AND contact present).
func TestManifest_ExampleBriefMatchesExport(t *testing.T) {
	t.Parallel()
	g := canonical(t)
	eb := g.doc.ExampleBrief
	if eb.Brief != exampleBrief {
		t.Fatalf("example_brief.brief = %q, want the case's example brief", eb.Brief)
	}
	gotRule, err := decodeAny(eb.Rule)
	if err != nil {
		t.Fatalf("example_brief.rule: %v", err)
	}
	wantRule, _ := decodeAny([]byte(exampleRule))
	if diff := cmp.Diff(wantRule, gotRule); diff != "" {
		t.Fatalf("example_brief.rule differs from segment-dsl.md §7.1 (-want +got):\n%s", diff)
	}

	evs := eventsByCustomer(g.events)
	var aud audienceDoc
	var consent consentDoc
	var longest longestDoc
	for _, c := range g.customers {
		if !matchesExample(c, evs[c.ID], canonicalRefTime) {
			continue
		}
		aud.Matched++
		if c.SMSOptIn && c.Phone != nil {
			aud.SMS++
		}
		if c.EmailOptIn && c.Email != nil {
			aud.Email++
		}
		addConsent(&consent, c)
		longest.FirstName = max(longest.FirstName, utf8.RuneCountInString(effectiveFirstName(c.FirstName)))
		longest.LastName = max(longest.LastName, utf8.RuneCountInString(c.LastName))
		longest.City = max(longest.City, utf8.RuneCountInString(c.City))
	}
	if aud.Matched == 0 {
		t.Fatal("no customer matches the example brief")
	}
	if diff := cmp.Diff(aud, eb.Audience); diff != "" {
		t.Errorf("example_brief.audience mismatch (-export +manifest):\n%s", diff)
	}
	if diff := cmp.Diff(consent, eb.Consent); diff != "" {
		t.Errorf("example_brief.consent mismatch (-export +manifest):\n%s", diff)
	}
	if diff := cmp.Diff(longest, eb.LongestValues); diff != "" {
		t.Errorf("example_brief.longest_values mismatch (-export +manifest):\n%s", diff)
	}
	// seed.md §3.3 / go-interfaces.md §content: the longest first_name is the 50-char long_first_name
	// group and the only matched city is İstanbul (8 runes).
	if eb.LongestValues.FirstName != 50 || eb.LongestValues.City != 8 {
		t.Errorf("example_brief.longest_values = %+v, want first_name 50 and city 8", eb.LongestValues)
	}
}

type groupSpec struct {
	count             int
	matches, sms, eml bool
	check             func(t *testing.T, c customer, evs []event, ref time.Time)
}

const longFirstName = "Şükriye Gülizar Çağla Ayşegül İpek Öykü Nilüfer Su"

// groupSpecs is the seed.md §3.3 table.
func groupSpecs() map[string]groupSpec {
	ist := func(c customer) bool { return c.City == "İstanbul" }
	return map[string]groupSpec{
		"cart_add_29d": {count: 5, matches: true, sms: true, eml: true, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			defaults(t, c, ist)
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(29))}, nil)
		}},
		"cart_add_30d": {count: 5, matches: true, sms: true, eml: true, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			defaults(t, c, ist)
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(30))}, nil)
		}},
		"cart_add_31d": {count: 5, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			defaults(t, c, ist)
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(31))}, nil)
		}},
		"cart_add_then_purchase": {count: 10, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			defaultsExceptPurchase(t, c, ist)
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(10))}, []time.Time{ref.Add(-days(5))})
		}},
		"purchase_before_cart_add": {count: 6, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			defaultsExceptPurchase(t, c, ist)
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(10))}, []time.Time{ref.Add(-days(20))})
		}},
		"istanbul_no_consent": {count: 10, matches: true, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			wantCustomer(t, c, "İstanbul", false, false, true, true)
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(7))}, nil)
		}},
		"istanbul_sms_only": {count: 8, matches: true, sms: true, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			wantCustomer(t, c, "İstanbul", true, false, true, true)
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(7))}, nil)
		}},
		"istanbul_email_only": {count: 8, matches: true, eml: true, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			wantCustomer(t, c, "İstanbul", false, true, true, true)
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(7))}, nil)
		}},
		"sms_consent_missing_phone": {count: 4, matches: true, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			wantCustomer(t, c, "İstanbul", true, false, false, true)
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(7))}, nil)
		}},
		"email_consent_missing_email": {count: 4, matches: true, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			wantCustomer(t, c, "İstanbul", false, true, true, false)
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(7))}, nil)
		}},
		"empty_first_name": {count: 6, matches: true, sms: true, eml: true, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			wantCustomer(t, c, "İstanbul", true, true, true, true)
			if c.FirstName != "" {
				t.Errorf("customer %s first_name = %q, want empty", c.ID, c.FirstName)
			}
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(7))}, nil)
		}},
		"long_first_name": {count: 3, matches: true, sms: true, eml: true, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			wantCustomer(t, c, "İstanbul", true, true, true, true)
			if c.FirstName != longFirstName {
				t.Errorf("customer %s first_name = %q, want %q", c.ID, c.FirstName, longFirstName)
			}
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(7))}, nil)
		}},
		"shared_contact": {count: 2, matches: true, sms: true, eml: true, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			defaults(t, c, ist)
			if c.Phone == nil || *c.Phone != "+905000009999" {
				t.Errorf("customer %s phone = %v, want +905000009999", c.ID, deref(c.Phone))
			}
			if c.Email == nil || *c.Email != "shared.contact@example.test" {
				t.Errorf("customer %s email = %v, want shared.contact@example.test", c.ID, deref(c.Email))
			}
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(7))}, nil)
		}},
		"non_istanbul_cart_add": {count: 10, check: func(t *testing.T, c customer, evs []event, ref time.Time) {
			defaults(t, c, func(c customer) bool { return c.City == "Ankara" })
			wantEvents(t, c, evs, []time.Time{ref.Add(-days(3))}, nil)
		}},
	}
}

// REQ-SEED-01
// Every edge-case group of seed.md §3.3 exists with its fixed size, flags and distinguishing data
// (29/30/31-day cart adds, cart-add-then-purchase, İstanbul without consent, empty and 50-char
// first_name, shared phone/email).
func TestManifest_EdgeCaseGroups(t *testing.T) {
	t.Parallel()
	g := canonical(t)
	assertGroups(t, g)
}

// REQ-SEED-01
// The fixed groups do not depend on --users: the minimum dataset (200) carries all of them.
func TestManifest_EdgeCaseGroupsAtMinimumUsers(t *testing.T) {
	t.Parallel()
	g := generate(t, seed.Options{Seed: canonicalSeed, Users: seed.MinUsers, RefTime: canonicalRefTime})
	if len(g.customers) != seed.MinUsers {
		t.Fatalf("customers = %d, want %d", len(g.customers), seed.MinUsers)
	}
	assertGroups(t, g)
}

func assertGroups(t *testing.T, g *generated) {
	t.Helper()
	ref := g.opts.RefTime
	byID := map[string]customer{}
	for _, c := range g.customers {
		byID[c.ID] = c
	}
	evs := eventsByCustomer(g.events)
	seen := map[string]string{}

	specs := groupSpecs()
	if len(g.doc.Groups) != len(specs) {
		t.Fatalf("manifest has %d groups, want %d", len(g.doc.Groups), len(specs))
	}
	for _, name := range groupNames {
		spec := specs[name]
		got, ok := g.doc.Groups[name]
		if !ok {
			t.Errorf("group %s missing from manifest", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			if got.Count != spec.count || len(got.CustomerIDs) != spec.count {
				t.Fatalf("count = %d, customer_ids = %d, want %d", got.Count, len(got.CustomerIDs), spec.count)
			}
			if got.MatchesExampleBrief != spec.matches || got.InSMSAudience != spec.sms || got.InEmailAudience != spec.eml {
				t.Fatalf("flags matches/sms/email = %v/%v/%v, want %v/%v/%v",
					got.MatchesExampleBrief, got.InSMSAudience, got.InEmailAudience, spec.matches, spec.sms, spec.eml)
			}
			if !slices.IsSorted(got.CustomerIDs) {
				t.Fatalf("customer_ids are not sorted ascending: %v", got.CustomerIDs)
			}
			for _, id := range got.CustomerIDs {
				if other, dup := seen[id]; dup {
					t.Fatalf("customer %s is in both %s and %s", id, other, name)
				}
				seen[id] = name
				c, ok := byID[id]
				if !ok {
					t.Fatalf("customer %s of group %s is not in the export", id, name)
				}
				spec.check(t, c, evs[id], ref)
				// The flags must agree with the data (independent recomputation).
				m := matchesExample(c, evs[id], ref)
				if m != spec.matches {
					t.Errorf("customer %s: recomputed match = %v, want %v", id, m, spec.matches)
				}
				if s := m && c.SMSOptIn && c.Phone != nil; s != spec.sms {
					t.Errorf("customer %s: recomputed sms audience = %v, want %v", id, s, spec.sms)
				}
				if e := m && c.EmailOptIn && c.Email != nil; e != spec.eml {
					t.Errorf("customer %s: recomputed email audience = %v, want %v", id, e, spec.eml)
				}
			}
		})
	}

	// Random population (seed.md §3.2): non-empty first_name <= 20 runes; events in
	// [ref-120d, ref-1h]; purchase amounts 50.00..5000.00.
	for _, c := range g.customers {
		if _, fixed := seen[c.ID]; fixed {
			continue
		}
		if n := utf8.RuneCountInString(c.FirstName); c.FirstName == "" || n > 20 {
			t.Errorf("random customer %s first_name %q (%d runes), want non-empty and <= 20", c.ID, c.FirstName, n)
		}
		for _, e := range evs[c.ID] {
			if e.at.Before(ref.Add(-days(120))) || e.at.After(ref.Add(-time.Hour)) {
				t.Errorf("random customer %s event %s at %s outside [ref-120d, ref-1h]", c.ID, e.ID, e.OccurredAt)
			}
			if e.Type == "purchase" && e.Amount != nil {
				if f, _ := e.Amount.Float64(); f < 50 || f > 5000 {
					t.Errorf("random customer %s purchase amount %s outside 50.00..5000.00", c.ID, e.Amount.String())
				}
			}
		}
	}
}

var (
	emailPattern = regexp.MustCompile(`^[a-z0-9._+-]+@example\.(com|test)$`)
	phonePattern = regexp.MustCompile(`^\+90500000[0-9]{4}$`)
)

// REQ-SEED-02
// Only example.com / example.test emails and the fictional +90 500 000 xx xx phone block; the
// shared phone/email appear only in shared_contact; every other phone is unique.
func TestDataset_FictionalContactData(t *testing.T) {
	t.Parallel()
	g := canonical(t)
	shared := map[string]bool{}
	for _, id := range g.doc.Groups["shared_contact"].CustomerIDs {
		shared[id] = true
	}
	long := map[string]bool{}
	for _, id := range g.doc.Groups["long_first_name"].CustomerIDs {
		long[id] = true
	}
	phones := map[string]string{}
	var maxFirst int
	for _, c := range g.customers {
		if c.Email != nil {
			if !emailPattern.MatchString(*c.Email) {
				t.Errorf("customer %s email %q is not a lowercase example.com/example.test address", c.ID, *c.Email)
			}
			if *c.Email == "shared.contact@example.test" && !shared[c.ID] {
				t.Errorf("customer %s uses the shared email outside shared_contact", c.ID)
			}
		}
		if c.Phone != nil {
			if !phonePattern.MatchString(*c.Phone) {
				t.Errorf("customer %s phone %q is outside the fictional block +90500000XXXX", c.ID, *c.Phone)
			}
			if *c.Phone == "+905000009999" {
				if !shared[c.ID] {
					t.Errorf("customer %s uses the reserved shared phone outside shared_contact", c.ID)
				}
			} else if other, dup := phones[*c.Phone]; dup {
				t.Errorf("phone %s is shared by %s and %s; only the shared_contact pair may share", *c.Phone, other, c.ID)
			} else {
				phones[*c.Phone] = c.ID
			}
		}
		n := utf8.RuneCountInString(c.FirstName)
		maxFirst = max(maxFirst, n)
		if n == 50 && !long[c.ID] {
			t.Errorf("customer %s has a 50-char first_name outside long_first_name", c.ID)
		}
	}
	if maxFirst != 50 {
		t.Errorf("longest first_name = %d runes, want 50 (long_first_name group)", maxFirst)
	}
}

// REQ-SEED-02
// No national id, birth date, gender, address or payment fields: a customer record carries exactly
// the seed.md §4 keys (parseJSONL rejects any other key), and none of them is sensitive.
func TestDataset_NoSensitivePersonalFields(t *testing.T) {
	t.Parallel()
	g := canonical(t)
	if len(g.customers) == 0 {
		t.Fatal("no customers")
	}
	forbidden := []string{"tc", "tckn", "national_id", "identity", "birth", "dob", "gender", "sex", "address", "street", "zip", "postal", "payment", "card", "iban"}
	for _, key := range customerKeys {
		for _, f := range forbidden {
			if strings.Contains(key, f) {
				t.Errorf("customer key %q looks like a sensitive field (%q)", key, f)
			}
		}
	}
	for _, key := range eventKeys {
		for _, f := range forbidden {
			if strings.Contains(key, f) {
				t.Errorf("event key %q looks like a sensitive field (%q)", key, f)
			}
		}
	}
}

// ---- data helpers ----

func addConsent(c *consentDoc, x customer) {
	switch {
	case x.SMSOptIn && x.EmailOptIn:
		c.Both++
	case x.SMSOptIn:
		c.SMSOnly++
	case x.EmailOptIn:
		c.EmailOnly++
	default:
		c.None++
	}
}

func eventsByCustomer(events []event) map[string][]event {
	m := map[string][]event{}
	for _, e := range events {
		m[e.CustomerID] = append(m[e.CustomerID], e)
	}
	return m
}

// matchesExample evaluates segment-dsl.md §7.1 with §4 semantics (lower bound inclusive).
func matchesExample(c customer, evs []event, ref time.Time) bool {
	if c.City != "İstanbul" {
		return false
	}
	lo := ref.Add(-days(30))
	cart := false
	for _, e := range evs {
		in := !e.at.Before(lo) && !e.at.After(ref)
		switch {
		case e.Type == "purchase" && in:
			return false
		case e.Type == "cart_add" && in:
			cart = true
		}
	}
	return cart
}

func effectiveFirstName(s string) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	return "Değerli müşterimiz"
}

// defaults asserts the seed.md §3.3 defaults: both consents, phone and email present,
// first_name <= 20 runes, no purchase.
func defaults(t *testing.T, c customer, city func(customer) bool) {
	t.Helper()
	defaultsExceptPurchase(t, c, city)
}

func defaultsExceptPurchase(t *testing.T, c customer, city func(customer) bool) {
	t.Helper()
	if !city(c) {
		t.Errorf("customer %s city = %q", c.ID, c.City)
	}
	if !c.SMSOptIn || !c.EmailOptIn {
		t.Errorf("customer %s sms_opt_in/email_opt_in = %v/%v, want true/true", c.ID, c.SMSOptIn, c.EmailOptIn)
	}
	if c.Phone == nil || c.Email == nil {
		t.Errorf("customer %s phone/email = %v/%v, want both present", c.ID, deref(c.Phone), deref(c.Email))
	}
	if n := utf8.RuneCountInString(c.FirstName); n > 20 {
		t.Errorf("customer %s first_name has %d runes, want <= 20", c.ID, n)
	}
}

func wantCustomer(t *testing.T, c customer, city string, smsOpt, emailOpt, phone, email bool) {
	t.Helper()
	if c.City != city {
		t.Errorf("customer %s city = %q, want %q", c.ID, c.City, city)
	}
	if c.SMSOptIn != smsOpt || c.EmailOptIn != emailOpt {
		t.Errorf("customer %s sms_opt_in/email_opt_in = %v/%v, want %v/%v", c.ID, c.SMSOptIn, c.EmailOptIn, smsOpt, emailOpt)
	}
	if (c.Phone != nil) != phone || (c.Email != nil) != email {
		t.Errorf("customer %s phone present/email present = %v/%v, want %v/%v", c.ID, c.Phone != nil, c.Email != nil, phone, email)
	}
}

// wantEvents asserts the customer's cart_add and purchase timestamps exactly (page views are free).
func wantEvents(t *testing.T, c customer, evs []event, cartAdds, purchases []time.Time) {
	t.Helper()
	var gotCart, gotPurchase []time.Time
	for _, e := range evs {
		switch e.Type {
		case "cart_add":
			gotCart = append(gotCart, e.at)
		case "purchase":
			gotPurchase = append(gotPurchase, e.at)
		}
	}
	sortTimes(gotCart)
	sortTimes(gotPurchase)
	sortTimes(cartAdds)
	sortTimes(purchases)
	if diff := cmp.Diff(cartAdds, gotCart, cmpTimes); diff != "" {
		t.Errorf("customer %s cart_add times (-want +got):\n%s", c.ID, diff)
	}
	if diff := cmp.Diff(purchases, gotPurchase, cmpTimes); diff != "" {
		t.Errorf("customer %s purchase times (-want +got):\n%s", c.ID, diff)
	}
}

var cmpTimes = cmp.Options{
	cmp.Comparer(func(a, b time.Time) bool { return a.Equal(b) }),
	cmp.Comparer(func(a, b []time.Time) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if !a[i].Equal(b[i]) {
				return false
			}
		}
		return true
	}),
}

func sortTimes(ts []time.Time) { slices.SortFunc(ts, func(a, b time.Time) int { return a.Compare(b) }) }

func deref(s *string) string {
	if s == nil {
		return "<null>"
	}
	return *s
}

func decodeAny(b []byte) (any, error) {
	var v any
	err := json.Unmarshal(b, &v)
	return v, err
}
