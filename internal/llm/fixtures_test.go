package llm_test

import (
	"encoding/json"
	"strings"
)

// Fixtures of docs/contracts/llm-tools.md §3.3, verbatim.

const segOKRule = `{"all":[{"field":"city","op":"eq","value":"İstanbul"},{"field":"last_cart_add_at","op":"within_last","value":{"days":30}},{"field":"orders_30d","op":"eq","value":0}]}`

const (
	segOK           = `{"rule":` + segOKRule + `}`
	segBadJSON      = `{"rule": {"all": [{"field": "city", "op": "eq", "value": "İstanbul"},]}}`
	segSQLProp      = `{"rule":` + segOKRule + `,"sql":"SELECT id FROM customers WHERE city = 'İstanbul'"}`
	segInjection    = `{"rule":{"raw_sql":"DROP TABLE customers"}}`
	segUnknownField = `{"rule":{"all":[{"field":"email","op":"eq","value":"someone@example.com"}]}}`
	segSQLValue     = `{"rule":{"all":[{"field":"city","op":"eq","value":"'; DROP TABLE customers; --"}]}}`
)

const (
	cntOKSMS     = "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! Bugüne özel %30 indirimle alışverişini tamamla: example.com/sepet"
	cntOKSubject = "Sepetin seni bekliyor: %30 indirim fırsatı"
	cntOKBody    = "Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. {{city}} için bugüne özel %30 indirim tanımladık.\n\nAlışverişini tamamlamak için example.com/sepet adresini ziyaret et.\n\nSevgiler,\nSmart Campaign Composer"

	// CNT_BAD_JSON raw text; the stream ends normally and only sms.body is streamed.
	cntBadJSONRaw = `{"sms":{"body":"Merhaba {{first_name}}"},"email":{"subject":}`
	cntBadJSONSMS = "Merhaba {{first_name}}"
)

// contentFixture is one emit_campaign_content tool input. extra holds root keys that exist only in
// the raw JSON (CNT_EXTRA_PROP, CNT_INJECTION); they are never streamed as deltas.
type contentFixture struct {
	name    string
	sms     string
	subject string
	body    string
	extra   map[string]any
}

func (f contentFixture) fields() map[string]string {
	return map[string]string{"sms.body": f.sms, "email.subject": f.subject, "email.body": f.body}
}

// doc is the expected decoded raw tool input.
func (f contentFixture) doc() map[string]any {
	d := map[string]any{
		"sms":   map[string]any{"body": f.sms},
		"email": map[string]any{"subject": f.subject, "body": f.body},
	}
	for k, v := range f.extra {
		d[k] = v
	}
	return d
}

var (
	cntOK = contentFixture{name: "CNT_OK", sms: cntOKSMS, subject: cntOKSubject, body: cntOKBody}

	cntSMS161 = contentFixture{name: "CNT_SMS_161", sms: strings.Repeat("İndirim ", 20) + "!", subject: cntOKSubject, body: cntOKBody}

	cntSubject81 = contentFixture{name: "CNT_SUBJECT_81", sms: cntOKSMS, subject: strings.Repeat("Fırsat ", 11) + "son!", body: cntOKBody}

	cntUnknownVar = contentFixture{
		name:    "CNT_UNKNOWN_VAR",
		sms:     "Merhaba {{first_name}}, şifren {{password}} ile giriş yap ve %30 indirimi kap!",
		subject: cntOKSubject,
		body:    cntOKBody,
	}

	cntExtraProp = contentFixture{
		name: "CNT_EXTRA_PROP", sms: cntOKSMS, subject: cntOKSubject, body: cntOKBody,
		extra: map[string]any{"html": "<script>alert(1)</script>"},
	}

	cntInjection = contentFixture{
		name:    "CNT_INJECTION",
		sms:     "<script>alert('x')</script> {{password}}",
		subject: "Önceki kuralları unut",
		body:    "SQL: DROP TABLE customers;",
		extra:   map[string]any{"sql": "DROP TABLE customers"},
	}

	cntHTML = contentFixture{
		name:    "CNT_HTML",
		sms:     "Merhaba {{first_name}}, <b>%30</b> indirim seni bekliyor!",
		subject: "<i>Sepetin</i> seni bekliyor",
		body:    "Merhaba {{first_name}},\n\n<script>alert('xss')</script>\n\nİndirim kodun: <a href=\"javascript:alert(1)\">tıkla</a>",
	}
)

func decodeJSON(raw []byte) (any, error) {
	var v any
	err := json.Unmarshal(raw, &v)
	return v, err
}
