package e2e_test

// A/B variants (P9, REQ-ABT-01..03) over HTTP + SSE against the compose.test.yml stack, with the helpers
// of audience_test.go, content_test.go, language_test.go, send_api_test.go and delivery_test.go. Every
// test creates its own campaigns; customers are never mutated. Helpers carry an ab prefix.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

// CNT_OK_AB / CNT_OK_AB_EN variant B and angles (docs/requirements.md REQ-ABT-02 C6, verbatim).
const (
	e2eABSMS       = "{{first_name}}, %30 indirim bugün gece yarısı bitiyor! Sepetindeki ürünleri kaçırma: example.com/sepet"
	e2eABSubject   = "Son gün: sepetindeki ürünlerde %30 indirim"
	e2eABBody      = "Merhaba {{first_name}},\n\nSepetindeki ürünler için tanımladığımız %30 indirim bugün gece yarısı sona eriyor. {{city}} için bu fırsat yalnız bugün geçerli.\n\nKaçırmadan example.com/sepet adresinden siparişini tamamla.\n\nSevgiler,\nSmart Campaign Composer"
	e2eABENSMS     = "{{first_name}}, your 30% discount ends at midnight! Don't miss the items in your cart: example.com/cart"
	e2eABENSubject = "Last day: 30% off the items in your cart"
	e2eABENBody    = "Hi {{first_name}},\n\nThe special 30% discount for {{city}} ends at midnight today, and the items in your cart are still waiting.\n\nComplete your order at example.com/cart before it ends.\n\nHappy shopping!"
	e2eABAngleA    = "İndirim oranı öne çıkar"
	e2eABAngleB    = "Aciliyet: indirim bugün bitiyor"
)

// Variant B texts of the A/B edits (every text differs from variant A; B's texts start with
// "B varyantı" / "Variant B" so a rendered preview shows which variant it is).
func abEditB() contentDoc {
	return mkContent("B varyantı {{first_name}}: %30 indirim bugün bitiyor.", "B varyantı: son gün", "Merhaba {{first_name}},\n\nB varyantı: indirim bugün bitiyor.")
}

func abEditBEN() contentDoc {
	return mkContent("Variant B {{first_name}}: 30% off ends today.", "Variant B: last day", "Hi {{first_name}},\n\nVariant B: the discount ends today.")
}

var abEditAngles = map[string]string{"a": "İndirim oranı öne çıkar", "b": "Aciliyet: indirim bugün bitiyor"}

// abBody is a content-versions request body: variant A (a, optional en), optional variant B (b,
// optional b.en) and optional angles (REQ-ABT-01 C1 shape).
func abBody(t *testing.T, a contentDoc, en, b, ben *contentDoc, angles map[string]string) []byte {
	t.Helper()
	doc := map[string]any{"sms": a.SMS, "email": a.Email}
	if en != nil {
		doc["en"] = map[string]any{"sms": en.SMS, "email": en.Email}
	}
	if b != nil {
		bm := map[string]any{"sms": b.SMS, "email": b.Email}
		if ben != nil {
			bm["en"] = map[string]any{"sms": ben.SMS, "email": ben.Email}
		}
		doc["b"] = bm
	}
	if angles != nil {
		doc["angles"] = angles
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

// contentJSON is a contentDoc in decoded-JSON form.
func contentJSON(c contentDoc) map[string]any {
	return map[string]any{"sms": map[string]any{"body": c.SMS.Body}, "email": map[string]any{"subject": c.Email.Subject, "body": c.Email.Body}}
}

// abSplit is the split rule of REQ-ABT-03 C1: the first byte of SHA-256(campaign UUID bytes ‖ customer
// UUID bytes) < 128 => "a", otherwise "b".
func abSplit(t *testing.T, campaignID, customerID string) string {
	t.Helper()
	raw := make([]byte, 0, 32)
	for _, u := range []string{campaignID, customerID} {
		b, err := hex.DecodeString(strings.ReplaceAll(u, "-", ""))
		if err != nil || len(b) != 16 {
			t.Fatalf("not a UUID: %q", u)
		}
		raw = append(raw, b...)
	}
	if sum := sha256.Sum256(raw); sum[0] < 128 {
		return "a"
	}
	return "b"
}

// abCheckByVariant checks CampaignStatus.by_variant (REQ-ABT-03 C7, C10): the key is always present;
// null, or {"a":{"sms","email"},"b":{"sms","email"}} of StatusCounts with a.c.k + b.c.k = by_channel.c.k.
func abCheckByVariant(t *testing.T, where string, status map[string]any) {
	t.Helper()
	bv, ok := status["by_variant"]
	if !ok {
		t.Fatalf("%s has no by_variant key: %v", where, status)
	}
	if bv == nil {
		return
	}
	m := asObject(t, bv)
	if diff := cmp.Diff([]string{"a", "b"}, sortedKeys(m)); diff != "" {
		t.Fatalf("%s by_variant keys mismatch (-want +got):\n%s", where, diff)
	}
	bc := asObject(t, status["by_channel"])
	for _, ch := range []string{"sms", "email"} {
		va := asObject(t, asObject(t, m["a"])[ch])
		vb := asObject(t, asObject(t, m["b"])[ch])
		total := asObject(t, bc[ch])
		for _, obj := range []map[string]any{va, vb} {
			if diff := cmp.Diff([]string{"failed", "queued", "sending", "sent", "total"}, sortedKeys(obj)); diff != "" {
				t.Fatalf("%s by_variant.*.%s keys mismatch (-want +got):\n%s", where, ch, diff)
			}
		}
		for _, k := range []string{"total", "queued", "sending", "sent", "failed"} {
			a, _ := va[k].(float64)
			b, _ := vb[k].(float64)
			if a+b != total[k] {
				t.Fatalf("%s by_variant.a.%s.%s + by_variant.b.%s.%s = %v + %v, want by_channel.%s.%s = %v", where, ch, k, ch, k, a, b, ch, k, total[k])
			}
		}
	}
}

// abStatus reads GET /status as a JSON object.
func abStatus(t *testing.T, api, id string) map[string]any {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/status", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET status = %d; body: %s", status, body)
	}
	obj := decodeObject(t, body)
	abCheckByVariant(t, "GET status", obj)
	return obj
}

// abDeliveries pages through GET /deliveries and returns the raw items.
func abDeliveries(t *testing.T, api, id string) []map[string]any {
	t.Helper()
	var all []map[string]any
	for offset := 0; ; offset += 200 {
		status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/deliveries?limit=200&offset="+strconv.Itoa(offset), nil, nil)
		if status != http.StatusOK {
			t.Fatalf("GET deliveries status = %d; body: %s", status, body)
		}
		var page struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		mustUnmarshal(t, string(body), &page)
		all = append(all, page.Items...)
		if len(page.Items) == 0 || len(all) >= page.Total {
			return all
		}
	}
}

// abWorst is the decoded worst_case of a validated event or a content version.
func abWorst(t *testing.T, data string) map[string]any {
	t.Helper()
	var v struct {
		WorstCase map[string]any `json:"worst_case"`
	}
	mustUnmarshal(t, data, &v)
	return v.WorstCase
}

// REQ-ABT-02 C1, C5, C6, C7, REQ-ABT-01 C7
// POST /content with {"ab": true} on the example audience (which has English recipients) streams and
// validates CNT_OK_AB_EN: variant A equals the A/B-less generation (sms, email, en and their
// worst_case), b / b.en / angles are the fixture texts, worst_case.b = {102 − 14 + L, 42} and
// worst_case.b.en = {103 − 14 + L, 40} with the manifest's longest first name L. B deltas carry
// variant "b" (B English also language "en") and follow every A delta; A deltas have no variant key;
// angles never stream. {} and {"ab": false} generate without B.
func TestE2E_GenerateABContent(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	L := m.ExampleBrief.LongestValues.FirstName

	ref := generate(t, api, audienceReady(t, api, m), "ok")
	if ref.validated == nil || ref.validatedEN(t) == nil {
		t.Fatalf("setup: the A/B-less ok generation did not validate bilingually")
	}
	refWC := abWorst(t, ref.events[len(ref.events)-1].Data)

	id := audienceReady(t, api, m)
	g := generateWith(t, api, id, "ok", []byte(`{"ab":true}`))
	if g.validated == nil || g.repair != nil {
		t.Fatalf("ab ok stream: validated %v repair %v, want only validated (final %+v)", g.validated, g.repair, g.final)
	}
	last := g.events[len(g.events)-1].Data
	var v struct {
		Content map[string]any `json:"content"`
	}
	mustUnmarshal(t, last, &v)
	c := v.Content
	if diff := cmp.Diff(contentJSON(okDoc()), map[string]any{"sms": c["sms"], "email": c["email"]}); diff != "" {
		t.Fatalf("variant A differs from CNT_OK (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(contentJSON(*ref.validatedEN(t)), c["en"]); diff != "" {
		t.Fatalf("variant A en differs from the A/B-less generation (-want +got):\n%s", diff)
	}
	wantB := contentJSON(mkContent(e2eABSMS, e2eABSubject, e2eABBody))
	wantB["en"] = contentJSON(mkContent(e2eABENSMS, e2eABENSubject, e2eABENBody))
	if diff := cmp.Diff(any(wantB), c["b"]); diff != "" {
		t.Fatalf("content.b mismatch with CNT_OK_AB_EN (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(any(map[string]any{"a": e2eABAngleA, "b": e2eABAngleB}), c["angles"]); diff != "" {
		t.Fatalf("content.angles mismatch (-want +got):\n%s", diff)
	}

	wc := abWorst(t, last)
	for _, k := range []string{"sms_runes", "subject_runes", "en"} {
		if diff := cmp.Diff(refWC[k], wc[k]); diff != "" {
			t.Fatalf("worst_case.%s differs from the A/B-less generation (-want +got):\n%s", k, diff)
		}
	}
	wantWCB := map[string]any{
		"sms_runes": float64(utf8.RuneCountInString(e2eABSMS) - len("{{first_name}}") + L), "subject_runes": float64(42),
		"en": map[string]any{"sms_runes": float64(utf8.RuneCountInString(e2eABENSMS) - len("{{first_name}}") + L), "subject_runes": float64(40)},
	}
	if diff := cmp.Diff(any(wantWCB), wc["b"]); diff != "" {
		t.Fatalf("worst_case.b mismatch (-want +got):\n%s", diff)
	}

	// Deltas: A Turkish, A English, B Turkish, B English; A deltas without a variant key.
	rank := -1
	for i, ev := range g.events {
		if ev.Name != "delta" {
			continue
		}
		var d map[string]any
		mustUnmarshal(t, ev.Data, &d)
		variant, hasVariant := d["variant"]
		if hasVariant && variant != "b" {
			t.Fatalf("event %d: delta variant = %v, want \"b\" or no key", i, variant)
		}
		r := 0
		if _, en := d["language"]; en {
			r = 1
		}
		if hasVariant {
			r += 2
		}
		if r < rank {
			t.Fatalf("event %d: delta group %d after group %d; order is A tr, A en, B tr, B en", i, r, rank)
		}
		rank = r
	}
	if rank != 3 {
		t.Fatalf("the last delta group = %d, want B English (3)", rank)
	}
	if diff := cmp.Diff(okDoc(), g.text(1)); diff != "" {
		t.Fatalf("A delta concatenation mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(mkContent(e2eABSMS, e2eABSubject, e2eABBody), g.textVariant(1, "b", "")); diff != "" {
		t.Fatalf("B delta concatenation mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(mkContent(e2eABENSMS, e2eABENSubject, e2eABENBody), g.textVariant(1, "b", "en")); diff != "" {
		t.Fatalf("B English delta concatenation mismatch (-want +got):\n%s", diff)
	}

	// The stored version carries the same B.
	cd := getCampaignDoc(t, api, id)
	if cd.LatestContentVersion == nil || cd.LatestContentVersion.ID != g.validated.ContentVersionID {
		t.Fatalf("latest version = %+v, want %s", cd.LatestContentVersion, g.validated.ContentVersionID)
	}

	for _, body := range []string{`{}`, `{"ab":false}`} {
		t.Run("body "+body+" generates without B", func(t *testing.T) {
			t.Parallel()
			g := generateWith(t, api, audienceReady(t, api, m), "ok", []byte(body))
			if g.validated == nil {
				t.Fatalf("stream did not validate (final %+v)", g.final)
			}
			var v struct {
				Content   map[string]any `json:"content"`
				WorstCase map[string]any `json:"worst_case"`
			}
			mustUnmarshal(t, g.events[len(g.events)-1].Data, &v)
			for _, k := range []string{"b", "angles"} {
				if _, ok := v.Content[k]; ok {
					t.Fatalf("content has %q without ab: %v", k, v.Content)
				}
			}
			if _, ok := v.WorstCase["b"]; ok {
				t.Fatalf("worst_case has b without ab: %v", v.WorstCase)
			}
			for _, d := range g.deltas {
				if d.Variant != "" {
					t.Fatalf("delta %+v carries a variant without ab", d)
				}
			}
		})
	}
}

// REQ-ABT-02 C8
// then_ok_after_repair with {"ab": true}: attempt 1 (CNT_SMS_161, B-less) fails, attempt 2 regenerates
// every variant: its deltas contain variant b and the validated content has b and angles; one stored
// version with llm_attempts 2.
func TestE2E_GenerateABRepair(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	g := generateWith(t, api, id, "then_ok_after_repair", []byte(`{"ab":true}`))
	if g.repair == nil || g.validated == nil || g.validated.Attempt != 2 {
		t.Fatalf("stream repair=%v validated=%v, want a repair and validated attempt 2", g.repair, g.validated)
	}
	if diff := cmp.Diff(mkContent(e2eABSMS, e2eABSubject, e2eABBody), g.textVariant(2, "b", "")); diff != "" {
		t.Fatalf("attempt 2 B delta concatenation mismatch (-want +got):\n%s", diff)
	}
	var v struct {
		Content map[string]any `json:"content"`
	}
	mustUnmarshal(t, g.events[len(g.events)-1].Data, &v)
	if v.Content["b"] == nil || v.Content["angles"] == nil {
		t.Fatalf("validated content after the repair = %v, want b and angles", v.Content)
	}
	list := listVersions(t, api, id)
	if len(list) != 1 || list[0].LLMAttempts == nil || *list[0].LLMAttempts != 2 {
		t.Fatalf("content-versions = %+v, want one version with llm_attempts 2", list)
	}
}

// REQ-ABT-01 C1, C2, C3, C4, C5, C7, C8
// A human edit carries b and angles through the same validator: an accepted A/B edit returns content.b,
// content.angles and worst_case.b (measured with the segment's longest values); an edit without b stores
// no B (the previous b is not kept). Invalid A/B content is 422 validation_failed at its path and stores
// nothing.
func TestE2E_EditVariantB(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	L := m.ExampleBrief.LongestValues.FirstName
	id := audienceReady(t, api, m)
	b := abEditB()

	v1 := mustEditRaw(t, api, id, "none", abBody(t, okDoc(), nil, &b, nil, abEditAngles))
	c1 := asObject(t, v1["content"])
	if diff := cmp.Diff(any(contentJSON(b)), c1["b"]); diff != "" {
		t.Fatalf("content.b mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(any(map[string]any{"a": abEditAngles["a"], "b": abEditAngles["b"]}), c1["angles"]); diff != "" {
		t.Fatalf("content.angles mismatch (-want +got):\n%s", diff)
	}
	wantWCB := map[string]any{
		"sms_runes":     float64(utf8.RuneCountInString(strings.ReplaceAll(b.SMS.Body, "{{first_name}}", "")) + L),
		"subject_runes": float64(utf8.RuneCountInString(b.Email.Subject)),
	}
	if diff := cmp.Diff(any(wantWCB), asObject(t, v1["worst_case"])["b"]); diff != "" {
		t.Fatalf("worst_case.b mismatch (-want +got):\n%s", diff)
	}

	v1ID, _ := v1["id"].(string)
	v2 := mustEditRaw(t, api, id, v1ID, okDoc().json(t))
	for _, k := range []string{"b", "angles"} {
		if _, ok := asObject(t, v2["content"])[k]; ok {
			t.Fatalf("edit without b returned content.%s: %v", k, v2["content"])
		}
	}
	if _, ok := asObject(t, v2["worst_case"])["b"]; ok {
		t.Fatalf("edit without b returned worst_case.b: %v", v2["worst_case"])
	}
	v2ID, _ := v2["id"].(string)

	long := abEditB()
	long.SMS.Body = strings.Repeat("b", 161)
	en := lngEN()
	ben := abEditBEN()
	tests := []struct {
		name string
		body []byte
		want []fieldWant
	}{
		{name: "b without angles", body: abBody(t, okDoc(), nil, &b, nil, nil), want: []fieldWant{{"angles", "schema_violation"}}},
		{name: "angles without b", body: abBody(t, okDoc(), nil, nil, nil, abEditAngles), want: []fieldWant{{"b", "schema_violation"}}},
		{name: "html inside b", body: []byte(`{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"b":{"sms":{"body":"x"},"email":{"subject":"y","body":"z"},"html":"<b>x</b>"},"angles":{"a":"A","b":"B"}}`), want: []fieldWant{{"b.html", "schema_violation"}}},
		{name: "en without b.en", body: abBody(t, okDoc(), &en, &b, nil, abEditAngles), want: []fieldWant{{"b.en", "schema_violation"}}},
		{name: "b.en without en", body: abBody(t, okDoc(), nil, &b, &ben, abEditAngles), want: []fieldWant{{"b.en", "schema_violation"}}},
		{name: "the criterion's two-error example", body: []byte(`{"sms":{"body":"x"},"email":{"subject":"y","body":"z"},"b":{"sms":{"body":"x"},"email":{"subject":"y"}}}`), want: []fieldWant{{"b.email.body", "schema_violation"}, {"angles", "schema_violation"}}},
		{name: "161-char b.sms.body", body: abBody(t, okDoc(), nil, &long, nil, abEditAngles), want: []fieldWant{{"b.sms.body", "sms_too_long"}}},
		{name: "61-char angles.a", body: abBody(t, okDoc(), nil, &b, nil, map[string]string{"a": strings.Repeat("a", 61), "b": "B"}), want: []fieldWant{{"angles.a", "too_long"}}},
		{name: "empty angles.b", body: abBody(t, okDoc(), nil, &b, nil, map[string]string{"a": "A", "b": " "}), want: []fieldWant{{"angles.b", "required"}}},
		{name: "B identical to A", body: abBody(t, okDoc(), nil, func() *contentDoc { d := okDoc(); return &d }(), nil, abEditAngles), want: []fieldWant{{"b", "variants_identical"}}},
	}
	t.Run("rejected", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				status, hdr, body := postEdit(t, api, id, v2ID, tc.body)
				e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
				for _, w := range tc.want {
					if !e.has(w) {
						t.Fatalf("error.fields = %+v, want %+v", e.Fields, w)
					}
				}
			})
		}
	})
	if list := listVersions(t, api, id); len(list) != 2 || list[0].ID != v2ID {
		t.Fatalf("rejected edits stored versions: %+v, want only v1 and v2", list)
	}
}

// REQ-ABT-03 C1, C5, C7, C10
// Sending an approved A/B version: before the send by_variant is present with zero counters; every
// delivery item carries variant (a|b) equal to the split rule for (campaign, customer); no customer gets
// two variants; 40 %–60 % of the customers get b; after completion by_variant.<v>.<c>.total is the
// number of deliveries of channel c and variant v, and a + b = by_channel for every counter.
func TestE2E_ABSendSplit(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	b := abEditB()
	v := mustEditRaw(t, api, id, "none", abBody(t, okDoc(), nil, &b, nil, abEditAngles))
	vid, _ := v["id"].(string)
	mustApprove(t, api, id, vid)

	before := abStatus(t, api, id)
	if before["by_variant"] == nil {
		t.Fatalf("approved A/B campaign by_variant = null, want zero counters")
	}
	for _, vv := range []string{"a", "b"} {
		for _, ch := range []string{"sms", "email"} {
			if total := asObject(t, asObject(t, asObject(t, before["by_variant"])[vv])[ch])["total"]; total != float64(0) {
				t.Fatalf("by_variant.%s.%s.total before the send = %v, want 0", vv, ch, total)
			}
		}
	}

	sndMustSend(t, api, id, vid)
	readStatusStream(t, api, id) // returns after the completed event

	items := abDeliveries(t, api, id)
	if len(items) != m.ExampleBrief.Audience.SMS+m.ExampleBrief.Audience.Email {
		t.Fatalf("deliveries = %d, want the channel audiences %d", len(items), m.ExampleBrief.Audience.SMS+m.ExampleBrief.Audience.Email)
	}
	perCustomer := map[string]string{}
	count := map[string]float64{}
	for i, it := range items {
		variant, ok := it["variant"].(string)
		if !ok || (variant != "a" && variant != "b") {
			t.Fatalf("deliveries items[%d] variant = %v, want a or b", i, it["variant"])
		}
		cust, _ := it["customer_id"].(string)
		ch, _ := it["channel"].(string)
		if want := abSplit(t, id, cust); variant != want {
			t.Fatalf("delivery of customer %s variant = %s, want %s (split rule)", cust, variant, want)
		}
		if prev, ok := perCustomer[cust]; ok && prev != variant {
			t.Fatalf("customer %s has deliveries of variants %s and %s", cust, prev, variant)
		}
		perCustomer[cust] = variant
		count[variant+"/"+ch]++
	}
	nb := 0
	for _, vv := range perCustomer {
		if vv == "b" {
			nb++
		}
	}
	if share := float64(nb) / float64(len(perCustomer)); share < 0.4 || share > 0.6 {
		t.Fatalf("share of customers with b = %d/%d = %.3f, want within 0.40–0.60", nb, len(perCustomer), share)
	}

	after := abStatus(t, api, id)
	if after["state"] != "completed" {
		t.Fatalf("state after the stream = %v, want completed", after["state"])
	}
	bv := asObject(t, after["by_variant"])
	for _, vv := range []string{"a", "b"} {
		for _, ch := range []string{"sms", "email"} {
			if got := asObject(t, asObject(t, bv[vv])[ch])["total"]; got != count[vv+"/"+ch] {
				t.Fatalf("by_variant.%s.%s.total = %v, want %v deliveries", vv, ch, got, count[vv+"/"+ch])
			}
		}
	}
}

// REQ-ABT-03 C10, C12
// GET …/preview takes an optional variant (a|b, default a) and always answers variant (shown) and
// customer.variant (the variant this customer gets at send time: the split rule for an A/B version, a
// otherwise). variant=b shows B, variant=b&language=en shows B's English text. On a version without b,
// variant=b is 422 variant_not_available at query.variant.
func TestE2E_PreviewVariant(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	en := lngEN()
	b := abEditB()
	ben := abEditBEN()
	v1 := mustEditRaw(t, api, id, "none", abBody(t, okDoc(), &en, &b, &ben, abEditAngles))

	type pv struct {
		Variant  string `json:"variant"`
		Customer struct {
			ID      string `json:"id"`
			Variant string `json:"variant"`
		} `json:"customer"`
		SMS struct {
			Text string `json:"text"`
		} `json:"sms"`
		Email struct {
			Subject string `json:"subject"`
		} `json:"email"`
	}
	get := func(t *testing.T, query string) pv {
		t.Helper()
		_, body := preview(t, api, id, query)
		raw := decodeObject(t, body)
		if _, ok := raw["variant"]; !ok {
			t.Fatalf("preview%s has no variant key: %s", query, body)
		}
		if _, ok := asObject(t, raw["customer"])["variant"]; !ok {
			t.Fatalf("preview%s has no customer.variant key: %s", query, body)
		}
		var p pv
		mustUnmarshal(t, string(body), &p)
		return p
	}
	tests := []struct {
		query, variant, smsPrefix string
	}{
		{query: "?language=tr", variant: "a", smsPrefix: "Merhaba "},
		{query: "?variant=a&language=tr", variant: "a", smsPrefix: "Merhaba "},
		{query: "?variant=b&language=tr", variant: "b", smsPrefix: "B varyantı "},
		{query: "?variant=b&language=en", variant: "b", smsPrefix: "Variant B "},
	}
	for _, tc := range tests {
		p := get(t, tc.query)
		if p.Variant != tc.variant || !strings.HasPrefix(p.SMS.Text, tc.smsPrefix) {
			t.Fatalf("preview%s variant/sms = %q/%q, want %q and the prefix %q", tc.query, p.Variant, p.SMS.Text, tc.variant, tc.smsPrefix)
		}
		if want := abSplit(t, id, p.Customer.ID); p.Customer.Variant != want {
			t.Fatalf("preview%s customer.variant = %q, want %q (split rule)", tc.query, p.Customer.Variant, want)
		}
	}
	if p := get(t, "?variant=b&language=tr"); p.Email.Subject != b.Email.Subject {
		t.Fatalf("variant b email.subject = %q, want %q", p.Email.Subject, b.Email.Subject)
	}

	// A version without b: variant=b is not available; the default is a, and every customer gets a.
	v1ID, _ := v1["id"].(string)
	mustEditRaw(t, api, id, v1ID, okDoc().json(t))
	if p := get(t, ""); p.Variant != "a" || p.Customer.Variant != "a" {
		t.Fatalf("preview of a version without b variant/customer.variant = %q/%q, want a/a", p.Variant, p.Customer.Variant)
	}
	status, hdr, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/preview?variant=b", nil, nil)
	e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
	if !e.has(fieldWant{"query.variant", "variant_not_available"}) {
		t.Fatalf("error.fields = %+v, want query.variant variant_not_available", e.Fields)
	}
}
