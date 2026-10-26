package llm_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/secret"
)

// fakeKey looks like an Anthropic key but is too short for the gitleaks rule; it is never valid.
const fakeKey = "sk-ant-test-0123"

// REQ-AI-03
func TestParseMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    llm.Mode
		wantErr error
	}{
		{name: "empty means auto", in: "", want: llm.ModeAuto},
		{name: "auto", in: "auto", want: llm.ModeAuto},
		{name: "real", in: "real", want: llm.ModeReal},
		{name: "mock", in: "mock", want: llm.ModeMock},
		{name: "unknown value is rejected", in: "fake", wantErr: llm.ErrInvalidMode},
		{name: "claude is a provider, not a mode", in: "claude", wantErr: llm.ErrInvalidMode},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := llm.ParseMode(tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ParseMode(%q) error = %v, want %v", tc.in, err, tc.wantErr)
			}
			if tc.wantErr == nil && got != tc.want {
				t.Fatalf("ParseMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// REQ-AI-03
// auto: key => claude, no key => mock; real without key => error; mock ignores the key.
func TestSelect_ProviderDecision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mode    llm.Mode
		key     string
		want    llm.Provider
		wantErr error
	}{
		{name: "auto with key selects claude", mode: llm.ModeAuto, key: fakeKey, want: llm.ProviderClaude},
		{name: "auto without key selects mock", mode: llm.ModeAuto, key: "", want: llm.ProviderMock},
		{name: "auto with whitespace-only key selects mock", mode: llm.ModeAuto, key: " \t\n", want: llm.ProviderMock},
		{name: "real with key selects claude", mode: llm.ModeReal, key: fakeKey, want: llm.ProviderClaude},
		{name: "real without key fails", mode: llm.ModeReal, key: "", wantErr: llm.ErrMissingAPIKey},
		{name: "real with whitespace-only key fails", mode: llm.ModeReal, key: "   ", wantErr: llm.ErrMissingAPIKey},
		{name: "mock without key selects mock", mode: llm.ModeMock, key: "", want: llm.ProviderMock},
		{name: "mock with key still selects mock", mode: llm.ModeMock, key: fakeKey, want: llm.ProviderMock},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := llm.Select(tc.mode, tc.key)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Select(%q, key) error = %v, want %v", tc.mode, err, tc.wantErr)
			}
			if tc.wantErr == nil && got != tc.want {
				t.Fatalf("Select(%q, key) = %q, want %q", tc.mode, got, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), fakeKey) {
				t.Fatalf("Select error contains the API key: %v", err)
			}
		})
	}
}

// REQ-AI-03, REQ-DLV-02
// Inside a test binary (testing.Testing() is true) llm.New never builds the real client:
// precedence 1 (mock), 2 (real => forbidden even without key), 3 (auto + key => forbidden).
func TestNew_SelectsMockOrRefusesRealClientInTests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		mode         llm.Mode
		key          string
		wantProvider llm.Provider
		wantErr      error
	}{
		{name: "mock without key builds the mock", mode: llm.ModeMock, wantProvider: llm.ProviderMock},
		{name: "mock with key builds the mock", mode: llm.ModeMock, key: fakeKey, wantProvider: llm.ProviderMock},
		{name: "auto without key builds the mock", mode: llm.ModeAuto, wantProvider: llm.ProviderMock},
		{name: "auto with whitespace-only key builds the mock", mode: llm.ModeAuto, key: "  ", wantProvider: llm.ProviderMock},
		{name: "auto with key is forbidden in tests", mode: llm.ModeAuto, key: fakeKey, wantErr: llm.ErrRealClientForbidden},
		{name: "real with key is forbidden in tests", mode: llm.ModeReal, key: fakeKey, wantErr: llm.ErrRealClientForbidden},
		{name: "real without key is forbidden in tests (before the missing-key check)", mode: llm.ModeReal, wantErr: llm.ErrRealClientForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			client, provider, err := llm.New(factoryConfig(tc.mode, tc.key, &logs))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("New() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if strings.Contains(err.Error(), fakeKey) {
					t.Fatalf("New() error contains the API key: %v", err)
				}
				return
			}
			if provider != tc.wantProvider {
				t.Fatalf("New() provider = %q, want %q", provider, tc.wantProvider)
			}
			if client == nil {
				t.Fatal("New() client = nil, want the mock client")
			}
			if _, ok := client.(*llm.Mock); !ok {
				t.Fatalf("New() client type = %T, want *llm.Mock", client)
			}
		})
	}
}

// REQ-AI-03, REQ-AI-04
// The selection is logged once at INFO ("llm provider selected", mode, provider, api_key_present);
// the key value never appears in the log.
func TestNew_LogsSelectionWithoutKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		mode           llm.Mode
		key            string
		wantMode       string
		wantProvider   string
		wantKeyPresent bool
	}{
		{name: "auto without key logs provider mock and no key", mode: llm.ModeAuto, wantMode: "auto", wantProvider: "mock", wantKeyPresent: false},
		{name: "mock with key logs key present but not its value", mode: llm.ModeMock, key: fakeKey, wantMode: "mock", wantProvider: "mock", wantKeyPresent: true},
		{name: "mock without key logs no key", mode: llm.ModeMock, wantMode: "mock", wantProvider: "mock", wantKeyPresent: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			if _, _, err := llm.New(factoryConfig(tc.mode, tc.key, &logs)); err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if strings.Contains(logs.String(), fakeKey) {
				t.Fatalf("log output contains the API key:\n%s", logs.String())
			}
			records := selectionRecords(t, &logs)
			if len(records) != 1 {
				t.Fatalf("got %d %q records, want exactly 1; log:\n%s", len(records), "llm provider selected", logs.String())
			}
			got := records[0]
			want := map[string]any{
				"level":           "INFO",
				"msg":             "llm provider selected",
				"mode":            tc.wantMode,
				"provider":        tc.wantProvider,
				"api_key_present": tc.wantKeyPresent,
			}
			subset := map[string]any{}
			for k := range want {
				subset[k] = got[k]
			}
			if diff := cmp.Diff(want, subset); diff != "" {
				t.Fatalf("selection log record mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-AI-04
// "never logs the key (also not on error)".
func TestNew_NeverLogsKeyOnError(t *testing.T) {
	t.Parallel()
	for _, mode := range []llm.Mode{llm.ModeAuto, llm.ModeReal} {
		t.Run(string(mode)+" with key", func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			_, _, err := llm.New(factoryConfig(mode, fakeKey, &logs))
			if err == nil {
				t.Fatal("New() error = nil, want ErrRealClientForbidden inside a test binary")
			}
			if strings.Contains(logs.String(), fakeKey) {
				t.Fatalf("log output contains the API key:\n%s", logs.String())
			}
		})
	}
}

func factoryConfig(mode llm.Mode, key string, logs *bytes.Buffer) llm.FactoryConfig {
	return llm.FactoryConfig{
		Mode:      mode,
		APIKey:    secret.Secret(key),
		Model:     "claude-opus-5-5",
		MaxTokens: 4096,
		Logger:    slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func selectionRecords(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(logs.Bytes()))
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line is not JSON (%v): %s", err, line)
		}
		if rec["msg"] == "llm provider selected" {
			out = append(out, rec)
		}
	}
	return out
}
