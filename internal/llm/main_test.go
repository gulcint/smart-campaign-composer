package llm_test

import (
	"testing"

	"github.com/gulcint/smart-campaign-composer/test/testenv"
)

// go-interfaces.md §0: every test package that builds an LLM client runs through testenv.Main.
func TestMain(m *testing.M) { testenv.Main(m) }
