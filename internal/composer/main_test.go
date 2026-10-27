package composer_test

import (
	"testing"

	"github.com/gulcint/smart-campaign-composer/test/testenv"
)

// REQ-AI-02, REQ-DLV-02: no real LLM client and no outbound network in this test binary.
func TestMain(m *testing.M) { testenv.Main(m) }
