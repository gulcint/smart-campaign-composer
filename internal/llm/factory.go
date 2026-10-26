package llm

import (
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/secret"
)

// FactoryConfig is everything New needs to pick and build a client.
type FactoryConfig struct {
	Mode       Mode
	APIKey     secret.Secret
	Model      string
	Timeout    time.Duration
	MaxTokens  int
	Mock       MockOptions
	HTTPClient *http.Client // nil => default
	Logger     *slog.Logger // nil => slog.Default()
}

// forbidEnv is set to "1" in compose.test.yml so that no process of the test stack can build
// the real client, even outside a Go test binary.
const forbidEnv = "SCC_FORBID_REAL_LLM"

// New selects and constructs the client. On success it logs exactly one INFO record
//
//	msg="llm provider selected" mode=<mode> provider=<mock|claude> api_key_present=<bool>
//
// and it never logs the key (also not on error). Error precedence (first match wins):
//  1. mode == ModeMock                      => Mock, ProviderMock, nil
//  2. forbidden && mode == ModeReal         => ErrRealClientForbidden (even without a key)
//  3. forbidden && mode == ModeAuto && key  => ErrRealClientForbidden
//  4. Select(mode, key) error               => ErrMissingAPIKey (real without key, not forbidden)
//  5. otherwise                             => Select's provider
func New(cfg FactoryConfig) (Client, Provider, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeAuto
	}
	keySet := cfg.APIKey.IsSet()
	forbidden := testing.Testing() || os.Getenv(forbidEnv) == "1"

	var (
		client   Client
		provider Provider
	)
	switch {
	case cfg.Mode == ModeMock:
		client, provider = NewMock(cfg.Mock), ProviderMock
	case forbidden && cfg.Mode == ModeReal:
		return nil, "", ErrRealClientForbidden
	case forbidden && cfg.Mode == ModeAuto && keySet:
		return nil, "", ErrRealClientForbidden
	default:
		p, err := Select(cfg.Mode, cfg.APIKey.Reveal())
		if err != nil {
			return nil, "", err
		}
		provider = p
		if p == ProviderMock {
			client = NewMock(cfg.Mock)
		} else {
			client = newClaude(cfg)
		}
	}

	logger.Info("llm provider selected",
		slog.String("mode", string(cfg.Mode)),
		slog.String("provider", string(provider)),
		slog.Bool("api_key_present", keySet),
	)
	return client, provider, nil
}
