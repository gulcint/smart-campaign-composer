package seed_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/seed"
)

// Canonical parameters (seed.md §2, docs/contracts/README.md §3).
var (
	canonicalSeed    int64 = 42
	canonicalUsers         = 5000
	canonicalRefTime       = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
)

const manifestPath = "../../testdata/seed/manifest.json"

const exampleBrief = "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

// exampleRule is segment-dsl.md §7.1 "rule" (root group).
const exampleRule = `{"all":[{"field":"city","op":"eq","value":"İstanbul"},{"field":"last_cart_add_at","op":"within_last","value":{"days":30}},{"field":"orders_30d","op":"eq","value":0}]}`

// ---- JSONL export (seed.md §4) ----

var customerKeys = []string{
	"kind", "id", "first_name", "last_name", "email", "phone", "city", "language", "loyalty_tier",
	"sms_opt_in", "sms_opt_in_at", "email_opt_in", "email_opt_in_at", "created_at", "last_seen_at",
}

var eventKeys = []string{"kind", "id", "customer_id", "type", "occurred_at", "amount", "product_category"}

type customer struct {
	Kind         string  `json:"kind"`
	ID           string  `json:"id"`
	FirstName    string  `json:"first_name"`
	LastName     string  `json:"last_name"`
	Email        *string `json:"email"`
	Phone        *string `json:"phone"`
	City         string  `json:"city"`
	Language     string  `json:"language"`
	LoyaltyTier  string  `json:"loyalty_tier"`
	SMSOptIn     bool    `json:"sms_opt_in"`
	SMSOptInAt   *string `json:"sms_opt_in_at"`
	EmailOptIn   bool    `json:"email_opt_in"`
	EmailOptInAt *string `json:"email_opt_in_at"`
	CreatedAt    string  `json:"created_at"`
	LastSeenAt   *string `json:"last_seen_at"`
}

type event struct {
	Kind            string       `json:"kind"`
	ID              string       `json:"id"`
	CustomerID      string       `json:"customer_id"`
	Type            string       `json:"type"`
	OccurredAt      string       `json:"occurred_at"`
	Amount          *json.Number `json:"amount"`
	ProductCategory *string      `json:"product_category"`

	at time.Time // parsed OccurredAt
}

// ---- manifest.json (seed.md §5) ----

var groupNames = []string{
	"cart_add_29d", "cart_add_30d", "cart_add_31d", "cart_add_then_purchase", "purchase_before_cart_add",
	"istanbul_no_consent", "istanbul_sms_only", "istanbul_email_only", "sms_consent_missing_phone",
	"email_consent_missing_email", "empty_first_name", "long_first_name", "shared_contact", "non_istanbul_cart_add",
	"english_recipients", // REQ-SEED-01 (P8)
}

// audienceDoc is an audience object of the manifest. Languages is present only in
// example_brief.audience (REQ-SEED-01, P8); reachable has no language split.
type audienceDoc struct {
	Matched   int           `json:"matched"`
	SMS       int           `json:"sms"`
	Email     int           `json:"email"`
	Languages *languagesDoc `json:"languages,omitempty"`
}

// languagesDoc is example_brief.audience.languages: per channel, the tr / en split of that
// channel's audience.
type languagesDoc struct {
	SMS   langSplit `json:"sms"`
	Email langSplit `json:"email"`
}

type langSplit struct {
	TR int `json:"tr"`
	EN int `json:"en"`
}

type consentDoc struct {
	Both      int `json:"both"`
	SMSOnly   int `json:"sms_only"`
	EmailOnly int `json:"email_only"`
	None      int `json:"none"`
}

type groupDoc struct {
	Count               int      `json:"count"`
	CustomerIDs         []string `json:"customer_ids"`
	MatchesExampleBrief bool     `json:"matches_example_brief"`
	InSMSAudience       bool     `json:"in_sms_audience"`
	InEmailAudience     bool     `json:"in_email_audience"`
}

type longestDoc struct {
	FirstName int `json:"first_name"`
	LastName  int `json:"last_name"`
	City      int `json:"city"`
}

type manifestDoc struct {
	SchemaVersion int `json:"schema_version"`
	Params        struct {
		Seed    int64  `json:"seed"`
		Users   int    `json:"users"`
		RefTime string `json:"ref_time"`
	} `json:"params"`
	DatasetSHA256 string `json:"dataset_sha256"`
	Totals        struct {
		Customers    int `json:"customers"`
		Events       int `json:"events"`
		EventsByType struct {
			CartAdd  int `json:"cart_add"`
			Purchase int `json:"purchase"`
			PageView int `json:"page_view"`
		} `json:"events_by_type"`
	} `json:"totals"`
	Consent consentDoc `json:"consent"`
	Contact struct {
		MissingPhone int `json:"missing_phone"`
		MissingEmail int `json:"missing_email"`
	} `json:"contact"`
	Reachable    audienceDoc    `json:"reachable"`
	Cities       map[string]int `json:"cities"`
	ExampleBrief struct {
		Brief         string          `json:"brief"`
		Rule          json.RawMessage `json:"rule"`
		Audience      audienceDoc     `json:"audience"`
		Consent       consentDoc      `json:"consent"`
		LongestValues longestDoc      `json:"longest_values"`
	} `json:"example_brief"`
	Groups map[string]groupDoc `json:"groups"`
}

// generated is one Generate run rendered through the contracted outputs.
type generated struct {
	opts      seed.Options
	jsonl     []byte
	manifest  []byte
	customers []customer
	events    []event
	doc       manifestDoc
}

var (
	canonicalOnce sync.Once
	canonicalGen  *generated
	canonicalErr  error
)

// canonical returns the dataset for --seed=42 --users=5000 --ref-time=2026-01-15T12:00:00Z (cached).
func canonical(t *testing.T) *generated {
	t.Helper()
	canonicalOnce.Do(func() {
		canonicalGen, canonicalErr = render(seed.Options{Seed: canonicalSeed, Users: canonicalUsers, RefTime: canonicalRefTime})
	})
	if canonicalErr != nil {
		t.Fatalf("canonical dataset: %v", canonicalErr)
	}
	return canonicalGen
}

func generate(t *testing.T, opts seed.Options) *generated {
	t.Helper()
	g, err := render(opts)
	if err != nil {
		t.Fatalf("dataset %+v: %v", opts, err)
	}
	return g
}

func render(opts seed.Options) (*generated, error) {
	d, err := seed.Generate(opts)
	if err != nil {
		return nil, fmt.Errorf("Generate: %w", err)
	}
	var buf bytes.Buffer
	if err := d.WriteJSONL(&buf); err != nil {
		return nil, fmt.Errorf("WriteJSONL: %w", err)
	}
	m, err := d.Manifest().MarshalCanonical()
	if err != nil {
		return nil, fmt.Errorf("MarshalCanonical: %w", err)
	}
	g := &generated{opts: opts, jsonl: buf.Bytes(), manifest: m}
	if g.customers, g.events, err = parseJSONL(g.jsonl); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(m))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g.doc); err != nil {
		return nil, fmt.Errorf("manifest does not match seed.md §5 schema: %w", err)
	}
	return g, nil
}

// parseJSONL decodes the export and checks line framing and key order (seed.md §4).
func parseJSONL(b []byte) ([]customer, []event, error) {
	if len(b) == 0 || b[len(b)-1] != '\n' {
		return nil, nil, fmt.Errorf("export must end with a newline")
	}
	var customers []customer
	var events []event
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	for i, line := range lines {
		keys, err := objectKeys([]byte(line))
		if err != nil {
			return nil, nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		var head struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			return nil, nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		switch head.Kind {
		case "customer":
			if len(events) > 0 {
				return nil, nil, fmt.Errorf("line %d: customer after events; customers must come first", i+1)
			}
			if !equalStrings(keys, customerKeys) {
				return nil, nil, fmt.Errorf("line %d: customer keys %v, want %v", i+1, keys, customerKeys)
			}
			var c customer
			if err := dec.Decode(&c); err != nil {
				return nil, nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			customers = append(customers, c)
		case "event":
			if !equalStrings(keys, eventKeys) {
				return nil, nil, fmt.Errorf("line %d: event keys %v, want %v", i+1, keys, eventKeys)
			}
			var e event
			if err := dec.Decode(&e); err != nil {
				return nil, nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			at, err := time.Parse(time.RFC3339, e.OccurredAt)
			if err != nil {
				return nil, nil, fmt.Errorf("line %d: occurred_at: %w", i+1, err)
			}
			e.at = at.UTC()
			events = append(events, e)
		default:
			return nil, nil, fmt.Errorf("line %d: unknown kind %q", i+1, head.Kind)
		}
	}
	return customers, events, nil
}

// objectKeys returns the top-level keys of a JSON object in document order.
func objectKeys(b []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected token %v", tok)
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readCommittedManifest(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(manifestPath))
	if err != nil {
		t.Fatalf("read %s (generated by `cmd/seed --skip-db`, committed by the implementer): %v", manifestPath, err)
	}
	return b
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }
