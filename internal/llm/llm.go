// Package llm hides the language model behind the Client interface (REQ-AI-02):
// provider selection (auto|real|mock), the real Claude client and the
// deterministic mock used by tests and key-less runs.
package llm

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// Mode is the configuration value of LLM_PROVIDER.
type Mode string

const (
	ModeAuto Mode = "auto"
	ModeReal Mode = "real"
	ModeMock Mode = "mock"
)

// ParseMode maps "" to ModeAuto; unknown values yield ErrInvalidMode.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case "":
		return ModeAuto, nil
	case ModeAuto, ModeReal, ModeMock:
		return Mode(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidMode, s)
}

// Provider is the effective provider, exposed by GET /api/config.
type Provider string

const (
	ProviderMock   Provider = "mock"
	ProviderClaude Provider = "claude"
)

// Select is the pure provider decision (REQ-AI-03):
//
//	auto + key set  => ProviderClaude; auto + no key => ProviderMock
//	real + key set  => ProviderClaude; real + no key => ErrMissingAPIKey
//	mock            => ProviderMock (key ignored)
func Select(mode Mode, apiKey string) (Provider, error) {
	keySet := strings.TrimSpace(apiKey) != ""
	switch mode {
	case ModeMock:
		return ProviderMock, nil
	case ModeAuto:
		if keySet {
			return ProviderClaude, nil
		}
		return ProviderMock, nil
	case ModeReal:
		if keySet {
			return ProviderClaude, nil
		}
		return "", ErrMissingAPIKey
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidMode, string(mode))
}

// ToolName values.
const (
	ToolEmitSegmentRule     = "emit_segment_rule"
	ToolEmitCampaignContent = "emit_campaign_content"
)

// The strict tool schemas are copies of docs/contracts/schemas/llm/*.tool.json
// (go:embed cannot reach outside the package directory); `make check-tool-schemas`
// fails when the copies drift from the contract.
//
//go:embed schemas/segment-rule.tool.json schemas/content.tool.json
var toolSchemas embed.FS

var toolSchemaFiles = map[string]string{
	ToolEmitSegmentRule:     "schemas/segment-rule.tool.json",
	ToolEmitCampaignContent: "schemas/content.tool.json",
}

// ToolSchema returns the strict-compatible input_schema SENT to the model for a tool.
// Unknown name => ErrUnknownTool. The backend never validates against this schema.
func ToolSchema(name string) (json.RawMessage, error) {
	file, ok := toolSchemaFiles[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
	b, err := toolSchemas.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("llm: read tool schema %s: %w", name, err)
	}
	return json.RawMessage(b), nil
}

// Client is one LLM attempt; validation and repair live in internal/composer.
type Client interface {
	// GenerateSegment returns the raw emit_segment_rule tool input (JSON bytes, possibly invalid).
	// Errors: ErrUnavailable, ErrTruncated, ErrNoToolCall, ctx errors.
	GenerateSegment(ctx context.Context, req SegmentRequest) (json.RawMessage, error)

	// StreamContent starts an emit_campaign_content generation.
	// Errors at start: ErrUnavailable, ctx errors.
	StreamContent(ctx context.Context, req ContentRequest) (ContentStream, error)
}

// Repair carries the rejected previous attempt into attempt 2.
type Repair struct {
	Output json.RawMessage         // previous raw tool input (may be invalid JSON)
	Errors []validation.FieldError // why it was rejected
}

type SegmentRequest struct {
	Brief    string
	Attempt  int     // 1 or 2
	Previous *Repair // non-nil iff Attempt == 2
}

type ContentRequest struct {
	Brief    string
	Rule     json.RawMessage // stored segment rule, for context
	Longest  content.Longest // current segment's longest values; told to the model as length budget
	Attempt  int
	Previous *Repair
}

type ContentChunk struct {
	Path string // "sms.body" | "email.subject" | "email.body"
	Text string // non-empty
}

type ContentStream interface {
	// Recv returns the next chunk; io.EOF when the model finished normally WITH a tool_use block;
	// ErrNoToolCall when the response ended without any emit_campaign_content tool_use block
	// (text only); ErrTruncated if the stream ended before the tool input was complete;
	// ErrUnavailable on transport failure; ctx errors on cancellation.
	Recv() (ContentChunk, error)
	// Raw returns the complete accumulated tool input. Meaningful only after Recv returned io.EOF.
	Raw() json.RawMessage
	Close() error
}

var (
	ErrUnavailable         = errors.New("llm: unavailable")
	ErrTruncated           = errors.New("llm: output truncated")
	ErrNoToolCall          = errors.New("llm: model did not call the tool")
	ErrInvalidMode         = errors.New("llm: invalid mode")
	ErrMissingAPIKey       = errors.New("llm: ANTHROPIC_API_KEY is required for LLM_PROVIDER=real")
	ErrRealClientForbidden = errors.New("llm: real client is forbidden in tests")
	ErrUnknownScenario     = errors.New("llm: unknown mock scenario")
	ErrUnknownTool         = errors.New("llm: unknown tool")
)
