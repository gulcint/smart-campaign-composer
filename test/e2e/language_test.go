package e2e_test

// Language-based content (P8, REQ-LNG-*) over HTTP + SSE against the compose.test.yml stack, with the
// helpers of audience_test.go and content_test.go. Every test creates its own campaigns; customers are
// never mutated.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

// audienceCounts checks the audience object of a segment or count response (REQ-LNG-02 C2: it
// carries `languages` = {"sms":{"tr","en"},"email":{"tr","en"}} with tr + en = the channel audience)
// and returns its matched / sms / email counts for the existing manifest comparisons.
func audienceCounts(t *testing.T, v any) map[string]any {
	t.Helper()
	a := asObject(t, v)
	if diff := cmp.Diff([]string{"email", "languages", "matched", "sms"}, sortedKeys(a)); diff != "" {
		t.Fatalf("audience keys mismatch (-want +got):\n%s", diff)
	}
	langs := asObject(t, a["languages"])
	if diff := cmp.Diff([]string{"email", "sms"}, sortedKeys(langs)); diff != "" {
		t.Fatalf("audience.languages keys mismatch (-want +got):\n%s", diff)
	}
	for _, ch := range []string{"sms", "email"} {
		l := asObject(t, langs[ch])
		if diff := cmp.Diff([]string{"en", "tr"}, sortedKeys(l)); diff != "" {
			t.Fatalf("audience.languages.%s keys mismatch (-want +got):\n%s", ch, diff)
		}
		tr, okTR := l["tr"].(float64)
		en, okEN := l["en"].(float64)
		if !okTR || !okEN || tr < 0 || en < 0 || tr+en != a[ch] {
			t.Fatalf("audience.languages.%s = %v, want non-negative tr + en = audience.%s (%v)", ch, l, ch, a[ch])
		}
	}
	return map[string]any{"matched": a["matched"], "sms": a["sms"], "email": a["email"]}
}

// langManifest is the P8 part of testdata/seed/manifest.json (REQ-SEED-01):
// example_brief.audience.languages and groups.english_recipients.
type langManifest struct {
	ExampleBrief struct {
		Audience struct {
			Languages map[string]map[string]int `json:"languages"`
		} `json:"audience"`
	} `json:"example_brief"`
	Groups struct {
		EnglishRecipients struct {
			Count       int      `json:"count"`
			CustomerIDs []string `json:"customer_ids"`
		} `json:"english_recipients"`
	} `json:"groups"`
}

func readLangManifest(t *testing.T) langManifest {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(manifestPath))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m langManifest
	mustUnmarshal(t, string(b), &m)
	l := m.ExampleBrief.Audience.Languages
	if l["sms"]["en"] < 6 || l["email"]["en"] < 6 {
		t.Fatalf("manifest example_brief.audience.languages = %v, want at least 6 en per channel (REQ-SEED-01)", l)
	}
	if g := m.Groups.EnglishRecipients; g.Count != 6 || len(g.CustomerIDs) != 6 {
		t.Fatalf("manifest groups.english_recipients = %+v, want 6 customers (REQ-SEED-01)", g)
	}
	return m
}

// languagesJSON is the manifest's languages in the decoded-JSON form of an API response.
func (m langManifest) languagesJSON() map[string]any {
	out := map[string]any{}
	for ch, l := range m.ExampleBrief.Audience.Languages {
		out[ch] = map[string]any{"tr": float64(l["tr"]), "en": float64(l["en"])}
	}
	return out
}

// bilingualBody is a content-versions request body: the Turkish blocks of c plus, when en != nil,
// the English block.
func bilingualBody(t *testing.T, c contentDoc, en *contentDoc) []byte {
	t.Helper()
	doc := map[string]any{"sms": c.SMS, "email": c.Email}
	if en != nil {
		doc["en"] = map[string]any{"sms": en.SMS, "email": en.Email}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

const (
	lngENSMS     = "Hi {{first_name}}, your cart is waiting with 30% off."
	lngENSubject = "Your cart is waiting"
	lngENBody    = "Hi {{first_name}},\n\nThe items in your cart are waiting for you."
)

func lngEN() contentDoc { return mkContent(lngENSMS, lngENSubject, lngENBody) }

// mustEditRaw posts a raw edit body and returns the 201 ContentVersion as a JSON object.
func mustEditRaw(t *testing.T, api, id, expected string, body []byte) map[string]any {
	t.Helper()
	status, _, resp := postEdit(t, api, id, expected, body)
	if status != http.StatusCreated {
		t.Fatalf("POST content-versions status = %d, want 201; body: %s", status, resp)
	}
	return decodeObject(t, resp)
}

// REQ-LNG-02 C2, REQ-SEED-01
// audience.languages has the same shape and values in POST …/segment, GET /api/campaigns/{id}
// (segment.audience) and POST /api/segments/count for the same rule, and equals the manifest's
// example_brief.audience.languages; each channel's tr + en equals that channel's audience.
func TestE2E_AudienceLanguages(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	lm := readLangManifest(t)
	id, _ := createCampaign(t, api, m.ExampleBrief.Brief)

	status, _, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("ok"))
	if status != http.StatusOK {
		t.Fatalf("POST segment status = %d; body: %s", status, body)
	}
	fromSegment := asObject(t, asObject(t, decodeObject(t, body)["segment"])["audience"])
	want := map[string]any{"matched": float64(m.ExampleBrief.Audience.Matched), "sms": float64(m.ExampleBrief.Audience.SMS), "email": float64(m.ExampleBrief.Audience.Email)}
	if diff := cmp.Diff(want, audienceCounts(t, fromSegment)); diff != "" {
		t.Fatalf("segment audience mismatch with the manifest (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(lm.languagesJSON(), fromSegment["languages"]); diff != "" {
		t.Fatalf("segment audience.languages mismatch with the manifest (-want +got):\n%s", diff)
	}

	status, _, body = doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET campaign status = %d; body: %s", status, body)
	}
	fromCampaign := asObject(t, asObject(t, decodeObject(t, body)["segment"])["audience"])

	status, _, body = doJSON(t, http.MethodPost, api+"/api/segments/count", []byte(`{"rule":`+string(m.ExampleBrief.Rule)+`}`), nil)
	if status != http.StatusOK {
		t.Fatalf("POST /api/segments/count status = %d; body: %s", status, body)
	}
	fromCount := asObject(t, decodeObject(t, body)["audience"])

	for name, got := range map[string]map[string]any{"campaign": fromCampaign, "count": fromCount} {
		audienceCounts(t, got)
		if diff := cmp.Diff(fromSegment["languages"], got["languages"]); diff != "" {
			t.Fatalf("%s audience.languages differs from the segment response (-segment +%s):\n%s", name, name, diff)
		}
	}
}

// REQ-LNG-01 C1, C2, C4, C5
// A human edit carries the optional `en` block through the same validator: an accepted bilingual
// edit returns content.en and worst_case.en (measured with the segment's longest values); an edit
// without `en` stores no English version (the previous one is not kept, no worst_case.en). Invalid
// English content is 422 validation_failed at its en-prefixed path and stores nothing.
func TestE2E_EditEnglishVersion(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	L := m.ExampleBrief.LongestValues.FirstName
	id := audienceReady(t, api, m)
	en := lngEN()

	v1 := mustEditRaw(t, api, id, "none", bilingualBody(t, okDoc(), &en))
	c1 := asObject(t, v1["content"])
	wantEN := map[string]any{"sms": map[string]any{"body": lngENSMS}, "email": map[string]any{"subject": lngENSubject, "body": lngENBody}}
	if diff := cmp.Diff(wantEN, c1["en"]); diff != "" {
		t.Fatalf("content.en mismatch (-want +got):\n%s", diff)
	}
	wc := asObject(t, v1["worst_case"])
	wantWCEN := map[string]any{
		"sms_runes":     float64(utf8.RuneCountInString(strings.ReplaceAll(lngENSMS, "{{first_name}}", "")) + L),
		"subject_runes": float64(utf8.RuneCountInString(lngENSubject)),
	}
	if diff := cmp.Diff(wantWCEN, wc["en"]); diff != "" {
		t.Fatalf("worst_case.en mismatch (-want +got):\n%s", diff)
	}
	if wc["sms_runes"] != float64(e2eSMSVarFree+L) {
		t.Fatalf("worst_case.sms_runes = %v, want the Turkish measurement %d unchanged", wc["sms_runes"], e2eSMSVarFree+L)
	}

	v1ID, _ := v1["id"].(string)
	v2 := mustEditRaw(t, api, id, v1ID, okDoc().json(t))
	if _, ok := asObject(t, v2["content"])["en"]; ok {
		t.Fatalf("edit without en returned content.en: %v", v2["content"])
	}
	if _, ok := asObject(t, v2["worst_case"])["en"]; ok {
		t.Fatalf("edit without en returned worst_case.en: %v", v2["worst_case"])
	}
	v2ID, _ := v2["id"].(string)

	long := lngEN()
	long.SMS.Body = strings.Repeat("a", 161)
	longSubj := lngEN()
	longSubj.Email.Subject = strings.Repeat("s", 81)
	unknown := lngEN()
	unknown.SMS.Body = "Your password {{password}}"
	tests := []struct {
		name string
		body []byte
		want fieldWant
	}{
		{name: "en null", body: []byte(`{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"en":null}`), want: fieldWant{"en", "schema_violation"}},
		{name: "en.sms missing", body: []byte(`{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"en":{"email":{"subject":"b","body":"c"}}}`), want: fieldWant{"en.sms", "schema_violation"}},
		{name: "html inside en", body: []byte(`{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"en":{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"html":"<b>x</b>"}}`), want: fieldWant{"en.html", "schema_violation"}},
		{name: "161-char English SMS", body: bilingualBody(t, okDoc(), &long), want: fieldWant{"en.sms.body", "sms_too_long"}},
		{name: "81-char English subject", body: bilingualBody(t, okDoc(), &longSubj), want: fieldWant{"en.email.subject", "subject_too_long"}},
		{name: "unknown variable in the English SMS", body: bilingualBody(t, okDoc(), &unknown), want: fieldWant{"en.sms.body", "unknown_variable"}},
	}
	// The group returns only after its parallel subtests finished.
	t.Run("rejected", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				status, hdr, body := postEdit(t, api, id, v2ID, tc.body)
				e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
				if !e.has(tc.want) {
					t.Fatalf("error.fields = %+v, want %+v", e.Fields, tc.want)
				}
			})
		}
	})
	if list := listVersions(t, api, id); len(list) != 2 || list[0].ID != v2ID {
		t.Fatalf("rejected edits stored versions: %+v, want only v1 and v2", list)
	}
}

// REQ-LNG-02 C1, C7, REQ-SEED-01
// The example audience has English recipients (manifest example_brief.audience.languages, at least 6
// en per channel), so content generation is bilingual: the stream carries Turkish deltas first (no
// language key), then English deltas with language "en"; the English deltas concatenate to the
// validated content.en, and worst_case.en is present.
func TestE2E_ContentLanguageFollowsAudience(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	readLangManifest(t)
	id := audienceReady(t, api, m)

	g := generate(t, api, id, "ok")
	if g.validated == nil {
		t.Fatalf("ok generation ended without validated (final %+v)", g.final)
	}
	seenEnglish := false
	en := map[string]string{}
	for i, ev := range g.events {
		if ev.Name != "delta" {
			continue
		}
		var d map[string]any
		mustUnmarshal(t, ev.Data, &d)
		lang, has := d["language"]
		if !has {
			if seenEnglish {
				t.Fatalf("event %d: Turkish delta after an English delta", i)
			}
			continue
		}
		if lang != "en" {
			t.Fatalf("event %d: delta language = %v, want \"en\" or no key", i, lang)
		}
		seenEnglish = true
		ch, _ := d["channel"].(string)
		f, _ := d["field"].(string)
		txt, _ := d["text"].(string)
		en[ch+"."+f] += txt
	}
	if !seenEnglish {
		t.Fatalf("no delta with language \"en\" although the audience has English recipients")
	}
	var v struct {
		Content struct {
			EN *contentDoc `json:"en"`
		} `json:"content"`
		WorstCase map[string]any `json:"worst_case"`
	}
	mustUnmarshal(t, g.events[len(g.events)-1].Data, &v)
	if v.Content.EN == nil {
		t.Fatalf("validated content has no en although the audience has English recipients")
	}
	want := map[string]string{"sms.body": v.Content.EN.SMS.Body, "email.subject": v.Content.EN.Email.Subject, "email.body": v.Content.EN.Email.Body}
	if diff := cmp.Diff(want, en); diff != "" {
		t.Fatalf("English delta concatenation differs from validated content.en (-want +got):\n%s", diff)
	}
	if _, ok := v.WorstCase["en"]; !ok {
		t.Fatalf("validated worst_case has no en: %v", v.WorstCase)
	}
}

type previewLangDoc struct {
	Language string `json:"language"`
	Fallback bool   `json:"fallback"`
	Customer struct {
		ID       string `json:"id"`
		Language string `json:"language"`
	} `json:"customer"`
	SMS struct {
		Text string `json:"text"`
	} `json:"sms"`
	Email struct {
		Subject string `json:"subject"`
	} `json:"email"`
}

func previewLang(t *testing.T, api, id, query string) previewLangDoc {
	t.Helper()
	_, body := preview(t, api, id, query)
	var p previewLangDoc
	mustUnmarshal(t, string(body), &p)
	return p
}

// REQ-LNG-03 C6, REQ-SEED-01
// GET …/preview takes an optional language (tr|en): the response carries language (shown),
// fallback (English asked or English recipient, but the version has no en => Turkish shown) and
// customer.language. language=tr is always Turkish; without the parameter the sample recipient's own
// language decides. An invalid value is 422 invalid_value at query.language.
func TestE2E_PreviewLanguage(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	en := lngEN()
	v1 := mustEditRaw(t, api, id, "none", bilingualBody(t, okDoc(), &en))

	type want struct {
		language string
		fallback bool
		smsHi    bool // English SMS ("Hi ") rather than Turkish ("Merhaba ")
	}
	check := func(t *testing.T, query string, w want) previewLangDoc {
		t.Helper()
		p := previewLang(t, api, id, query)
		if !slices.Contains([]string{"tr", "en"}, p.Customer.Language) {
			t.Fatalf("preview%s customer.language = %q, want tr or en", query, p.Customer.Language)
		}
		if p.Language != w.language || p.Fallback != w.fallback {
			t.Fatalf("preview%s language/fallback = %q/%v, want %q/%v", query, p.Language, p.Fallback, w.language, w.fallback)
		}
		if got := strings.HasPrefix(p.SMS.Text, "Hi "); got != w.smsHi || (!w.smsHi && !strings.HasPrefix(p.SMS.Text, "Merhaba ")) {
			t.Fatalf("preview%s sms.text = %q, want the %s text", query, p.SMS.Text, w.language)
		}
		return p
	}

	// An English recipient of the manifest (groups.english_recipients, REQ-SEED-01).
	enCustomer := "?customer_id=" + readLangManifest(t).Groups.EnglishRecipients.CustomerIDs[0]

	// Bilingual version.
	check(t, "?language=tr", want{language: "tr"})
	check(t, "?language=en", want{language: "en", smsHi: true})
	own := previewLang(t, api, id, "")
	check(t, "", want{language: own.Customer.Language, smsHi: own.Customer.Language == "en"})
	if p := check(t, enCustomer, want{language: "en", smsHi: true}); p.Customer.Language != "en" {
		t.Fatalf("english_recipients customer.language = %q, want en", p.Customer.Language)
	}
	check(t, enCustomer+"&language=tr", want{language: "tr"})

	// Turkish-only version: English is a fallback to Turkish.
	mustEditRaw(t, api, id, v1["id"].(string), okDoc().json(t))
	check(t, "?language=en", want{language: "tr", fallback: true})
	check(t, "?language=tr", want{language: "tr"})
	check(t, "", want{language: "tr", fallback: own.Customer.Language == "en"})
	check(t, enCustomer, want{language: "tr", fallback: true})

	for _, q := range []string{"?language=de", "?language=fr"} {
		t.Run("invalid "+q, func(t *testing.T) {
			status, hdr, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/preview"+q, nil, nil)
			e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
			if !e.has(fieldWant{"query.language", "invalid_value"}) {
				t.Fatalf("error.fields = %+v, want query.language invalid_value", e.Fields)
			}
		})
	}
}
