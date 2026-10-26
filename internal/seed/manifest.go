package seed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/content"
)

// ExampleBrief is the case document's example brief (seed.md §5).
const ExampleBrief = "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

// exampleRule is exactly the "rule" value of segment-dsl.md §7.1.
const exampleRule = `{"all":[{"field":"city","op":"eq","value":"İstanbul"},{"field":"last_cart_add_at","op":"within_last","value":{"days":30}},{"field":"orders_30d","op":"eq","value":0}]}`

// GroupNames lists the fixed edge groups in manifest order (seed.md §3.3, §5).
var GroupNames = []string{
	"cart_add_29d", "cart_add_30d", "cart_add_31d", "cart_add_then_purchase", "purchase_before_cart_add",
	"istanbul_no_consent", "istanbul_sms_only", "istanbul_email_only", "sms_consent_missing_phone",
	"email_consent_missing_email", "empty_first_name", "long_first_name", "shared_contact", "non_istanbul_cart_add",
}

// expectedGroups is the seed.md §3.3 table (matches example brief, in SMS audience, in email
// audience); Generate refuses to produce a dataset that contradicts it.
var expectedGroups = map[string][3]bool{
	"cart_add_29d":                {true, true, true},
	"cart_add_30d":                {true, true, true},
	"cart_add_31d":                {false, false, false},
	"cart_add_then_purchase":      {false, false, false},
	"purchase_before_cart_add":    {false, false, false},
	"istanbul_no_consent":         {true, false, false},
	"istanbul_sms_only":           {true, true, false},
	"istanbul_email_only":         {true, false, true},
	"sms_consent_missing_phone":   {true, false, false},
	"email_consent_missing_email": {true, false, false},
	"empty_first_name":            {true, true, true},
	"long_first_name":             {true, true, true},
	"shared_contact":              {true, true, true},
	"non_istanbul_cart_add":       {false, false, false},
}

// Manifest mirrors the manifest schema in seed.md §5. Field order is the canonical key order.
type Manifest struct {
	SchemaVersion int              `json:"schema_version"`
	Params        Params           `json:"params"`
	DatasetSHA256 string           `json:"dataset_sha256"`
	Totals        Totals           `json:"totals"`
	Consent       ConsentBreakdown `json:"consent"`
	Contact       Contact          `json:"contact"`
	Reachable     Audience         `json:"reachable"`
	Cities        map[string]int   `json:"cities"`
	ExampleBrief  ExampleBriefInfo `json:"example_brief"`
	Groups        Groups           `json:"groups"`
}

type Params struct {
	Seed    int64  `json:"seed"`
	Users   int    `json:"users"`
	RefTime string `json:"ref_time"`
}

type Totals struct {
	Customers    int          `json:"customers"`
	Events       int          `json:"events"`
	EventsByType EventsByType `json:"events_by_type"`
}

type EventsByType struct {
	CartAdd  int `json:"cart_add"`
	Purchase int `json:"purchase"`
	PageView int `json:"page_view"`
}

type ConsentBreakdown struct {
	Both      int `json:"both"`
	SMSOnly   int `json:"sms_only"`
	EmailOnly int `json:"email_only"`
	None      int `json:"none"`
}

type Contact struct {
	MissingPhone int `json:"missing_phone"`
	MissingEmail int `json:"missing_email"`
}

type Audience struct {
	Matched int `json:"matched"`
	SMS     int `json:"sms"`
	Email   int `json:"email"`
}

type ExampleBriefInfo struct {
	Brief         string           `json:"brief"`
	Rule          json.RawMessage  `json:"rule"`
	Audience      Audience         `json:"audience"`
	Consent       ConsentBreakdown `json:"consent"`
	LongestValues content.Longest  `json:"longest_values"`
}

type Group struct {
	Count               int      `json:"count"`
	CustomerIDs         []string `json:"customer_ids"`
	MatchesExampleBrief bool     `json:"matches_example_brief"`
	InSMSAudience       bool     `json:"in_sms_audience"`
	InEmailAudience     bool     `json:"in_email_audience"`
}

// Groups maps a group name to its group; it marshals in GroupNames order.
type Groups map[string]Group

// MarshalJSON emits the groups in canonical (GroupNames) order.
func (g Groups) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, name := range GroupNames {
		grp, ok := g[name]
		if !ok {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		k, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		v, err := marshalNoEscape(grp)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// MarshalCanonical: 2-space indent, keys in the order defined in seed.md, trailing newline.
func (m Manifest) MarshalCanonical() ([]byte, error) {
	compact, err := marshalNoEscape(m)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

func marshalNoEscape(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func consentOf(c *customer, bd *ConsentBreakdown) {
	switch {
	case c.smsOptIn && c.emailOptIn:
		bd.Both++
	case c.smsOptIn:
		bd.SMSOnly++
	case c.emailOptIn:
		bd.EmailOnly++
	default:
		bd.None++
	}
}

func buildManifest(opts Options, customers []customer, sha string) (Manifest, error) {
	m := Manifest{
		SchemaVersion: 1,
		Params:        Params{Seed: opts.Seed, Users: opts.Users, RefTime: opts.RefTime.Format(time.RFC3339)},
		DatasetSHA256: sha,
		Cities:        map[string]int{},
		ExampleBrief:  ExampleBriefInfo{Brief: ExampleBrief, Rule: json.RawMessage(exampleRule)},
		Groups:        Groups{},
	}
	longest := content.Longest{}
	matched := 0
	members := map[string][]*customer{}
	for i := range customers {
		c := &customers[i]
		m.Totals.Customers++
		for _, e := range c.events {
			m.Totals.Events++
			switch e.typ {
			case "cart_add":
				m.Totals.EventsByType.CartAdd++
			case "purchase":
				m.Totals.EventsByType.Purchase++
			case "page_view":
				m.Totals.EventsByType.PageView++
			}
		}
		consentOf(c, &m.Consent)
		if c.phone == nil {
			m.Contact.MissingPhone++
		}
		if c.email == nil {
			m.Contact.MissingEmail++
		}
		m.Reachable.Matched++
		if c.smsOptIn && c.phone != nil {
			m.Reachable.SMS++
		}
		if c.emailOptIn && c.email != nil {
			m.Reachable.Email++
		}
		m.Cities[c.city]++

		if c.matchesBrief {
			matched++
			m.ExampleBrief.Audience.Matched++
			consentOf(c, &m.ExampleBrief.Consent)
			longest.FirstName = max(longest.FirstName, content.RuneCount(content.EffectiveFirstName(c.firstName)))
			longest.LastName = max(longest.LastName, content.RuneCount(c.lastName))
			longest.City = max(longest.City, content.RuneCount(c.city))
		}
		if c.inSMSAudience {
			m.ExampleBrief.Audience.SMS++
		}
		if c.inEmailAud {
			m.ExampleBrief.Audience.Email++
		}
		if c.group != "" {
			members[c.group] = append(members[c.group], c)
		}
	}
	if matched == 0 {
		longest = content.Longest{FirstName: content.RuneCount(content.FirstNameFallback)}
	}
	m.ExampleBrief.LongestValues = longest

	for _, name := range GroupNames {
		cs := members[name]
		if len(cs) == 0 {
			return Manifest{}, fmt.Errorf("seed: group %s is empty", name)
		}
		grp := Group{
			Count:               len(cs),
			MatchesExampleBrief: cs[0].matchesBrief,
			InSMSAudience:       cs[0].inSMSAudience,
			InEmailAudience:     cs[0].inEmailAud,
		}
		for _, c := range cs {
			if c.matchesBrief != grp.MatchesExampleBrief || c.inSMSAudience != grp.InSMSAudience || c.inEmailAud != grp.InEmailAudience {
				return Manifest{}, fmt.Errorf("seed: members of group %s disagree (customer index %s)", name, strconv.Itoa(c.index))
			}
			grp.CustomerIDs = append(grp.CustomerIDs, c.id)
		}
		if want := expectedGroups[name]; want != [3]bool{grp.MatchesExampleBrief, grp.InSMSAudience, grp.InEmailAudience} {
			return Manifest{}, fmt.Errorf("seed: group %s contradicts seed.md §3.3", name)
		}
		slices.Sort(grp.CustomerIDs)
		m.Groups[name] = grp
	}
	return m, nil
}
