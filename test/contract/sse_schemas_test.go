package contract_test

import (
	"testing"
)

// REQ-CNT-04
// "Her delta/invalid/validated olayının JSON alanları openapi.yaml bileşenlerinde (ContentDeltaEvent,
// ContentInvalidEvent, ContentValidatedEvent) tanımlıdır": the components accept the event shapes
// the requirement describes and refuse ones it forbids.
func TestSSEEventSchemas(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	okContent := `{"sms":{"body":"Merhaba {{first_name}}"},"email":{"subject":"Sepetin seni bekliyor","body":"Merhaba"}}`
	tests := []struct {
		name   string
		schema string
		data   string
		valid  bool
	}{
		{name: "delta sms body", schema: "ContentDeltaEvent", data: `{"attempt":1,"channel":"sms","field":"body","text":"Merhaba {{first_"}`, valid: true},
		{name: "delta email subject", schema: "ContentDeltaEvent", data: `{"attempt":2,"channel":"email","field":"subject","text":"Sepetin"}`, valid: true},
		{name: "delta sms subject is not a valid pair", schema: "ContentDeltaEvent", data: `{"attempt":1,"channel":"sms","field":"subject","text":"x"}`},
		{name: "delta empty text", schema: "ContentDeltaEvent", data: `{"attempt":1,"channel":"sms","field":"body","text":""}`},
		{name: "delta attempt 3", schema: "ContentDeltaEvent", data: `{"attempt":3,"channel":"sms","field":"body","text":"x"}`},
		{name: "delta extra key", schema: "ContentDeltaEvent", data: `{"attempt":1,"channel":"sms","field":"body","text":"x","id":1}`},
		{name: "non-final invalid", schema: "ContentInvalidEvent", data: `{"attempt":1,"final":false,"errors":[{"path":"sms.body","code":"sms_too_long","message":"m","limit":160,"actual":161}],"content_version_id":null}`, valid: true},
		{name: "final invalid with a stored draft", schema: "ContentInvalidEvent", data: `{"attempt":2,"final":true,"errors":[{"path":"sms.body","code":"sms_too_long","message":"m","limit":160,"actual":161}],"content_version_id":"5c1d7e3a-2b4f-4c6d-8e9f-0a1b2c3d4e5f"}`, valid: true},
		{name: "final invalid llm_unavailable on attempt 1", schema: "ContentInvalidEvent", data: `{"attempt":1,"final":true,"errors":[{"path":"","code":"llm_unavailable","message":"m"}],"content_version_id":null}`, valid: true},
		{name: "non-final invalid with a version id", schema: "ContentInvalidEvent", data: `{"attempt":1,"final":false,"errors":[{"path":"","code":"invalid_json","message":"m"}],"content_version_id":"5c1d7e3a-2b4f-4c6d-8e9f-0a1b2c3d4e5f"}`},
		{name: "non-final invalid on attempt 2", schema: "ContentInvalidEvent", data: `{"attempt":2,"final":false,"errors":[{"path":"","code":"invalid_json","message":"m"}],"content_version_id":null}`},
		{name: "invalid without errors", schema: "ContentInvalidEvent", data: `{"attempt":2,"final":true,"errors":[],"content_version_id":null}`},
		{name: "invalid without content_version_id key", schema: "ContentInvalidEvent", data: `{"attempt":2,"final":true,"errors":[{"path":"","code":"invalid_json","message":"m"}]}`},
		{name: "validated", schema: "ContentValidatedEvent", data: `{"attempt":1,"content_version_id":"0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11","version_number":3,"content":` + okContent + `,"worst_case":{"sms_runes":160,"subject_runes":42}}`, valid: true},
		{name: "validated with empty sms body", schema: "ContentValidatedEvent", data: `{"attempt":1,"content_version_id":"0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11","version_number":3,"content":{"sms":{"body":""},"email":{"subject":"a","body":"b"}},"worst_case":{"sms_runes":0,"subject_runes":1}}`},
		{name: "validated with an html key in content", schema: "ContentValidatedEvent", data: `{"attempt":1,"content_version_id":"0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11","version_number":3,"content":{"sms":{"body":"a"},"email":{"subject":"a","body":"b"},"html":"<b>x</b>"},"worst_case":{"sms_runes":1,"subject_runes":1}}`},
		{name: "validated without worst_case", schema: "ContentValidatedEvent", data: `{"attempt":1,"content_version_id":"0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11","version_number":3,"content":` + okContent + `}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			problems := spec.ValidateComponent(tc.schema, []byte(tc.data))
			if tc.valid && len(problems) > 0 {
				t.Fatalf("%s rejected a valid payload: %v", tc.schema, problems)
			}
			if !tc.valid && len(problems) == 0 {
				t.Fatalf("%s accepted an invalid payload: %s", tc.schema, tc.data)
			}
		})
	}
}
