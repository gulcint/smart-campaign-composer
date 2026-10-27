package contract_test

import (
	"testing"

	"github.com/gulcint/smart-campaign-composer/test/testenv/apispec"
)

// loadSpec reads docs/contracts/openapi.yaml from disk (OPENAPI_SPEC_PATH or the module root).
func loadSpec(t *testing.T) *apispec.Spec {
	t.Helper()
	p, err := apispec.SpecPath()
	if err != nil {
		t.Fatalf("locate openapi.yaml: %v", err)
	}
	s, err := apispec.Load(p)
	if err != nil {
		t.Fatalf("load %s: %v", p, err)
	}
	return s
}
