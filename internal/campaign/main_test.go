package campaign_test

import (
	"testing"

	"github.com/gulcint/smart-campaign-composer/test/testenv"
)

// REQ-AI-02, REQ-DLV-02: the service integration tests build LLM clients (llm.Mock and scripted
// fakes); no real LLM client and no outbound network in this test binary.
func TestMain(m *testing.M) { testenv.Main(m) }
