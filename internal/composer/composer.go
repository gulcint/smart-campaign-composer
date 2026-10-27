// Package composer runs LLM generation with validation and the single repair attempt
// (docs/contracts/llm-tools.md §2): GenerateSegment for the segment rule, OpenContent +
// ContentSession.Run for the streamed content (content.go).
package composer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/metrics"
	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// ErrInvalidOutput: the model output was still invalid after the single repair attempt.
// The returned error also unwraps to the last attempt's *validation.Error.
var ErrInvalidOutput = errors.New("composer: llm output invalid after repair")

// Event is one content-stream SSE event (sse-events.md §1). Name() is the SSE event name.
type Event interface{ Name() string }

// SegmentOutcome is a validated segment rule and the number of LLM attempts it took.
type SegmentOutcome struct {
	Rule     segment.Rule
	Attempts int // 1 or 2
}

const maxAttempts = 2

// GenerateSegment runs attempt 1, validates (segment.Parse + segment.Validate), and on failure
// runs exactly one repair attempt carrying the rejected output and its field errors.
// Errors: ErrInvalidOutput (wrapping *validation.Error), llm.ErrUnavailable, ctx errors.
// llm.ErrTruncated => "truncated_output", llm.ErrNoToolCall => "no_tool_call" (path ""); both are
// invalid output and trigger the repair attempt. The raw model output is never returned to the
// caller: only a validated segment.Rule or field errors leave this function.
func GenerateSegment(ctx context.Context, c llm.Client, brief string) (SegmentOutcome, error) {
	provider := string(llm.ProviderOf(c))
	var prev *llm.Repair
	var last *validation.Error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			metrics.ObserveLLMRepair("segment")
		}
		raw, err := c.GenerateSegment(ctx, llm.SegmentRequest{Brief: brief, Attempt: attempt, Previous: prev})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return SegmentOutcome{}, ctxErr
			}
			fe, invalid := clientErrorField(err)
			if !invalid {
				metrics.ObserveLLMAttempt("segment", provider, "unavailable")
				if errors.Is(err, llm.ErrUnavailable) {
					return SegmentOutcome{}, err
				}
				return SegmentOutcome{}, fmt.Errorf("%w: %w", llm.ErrUnavailable, err)
			}
			last = &validation.Error{Fields: []validation.FieldError{fe}}
		} else {
			rule, verr := parseAndValidate(raw)
			if verr == nil {
				metrics.ObserveLLMAttempt("segment", provider, "valid")
				return SegmentOutcome{Rule: rule, Attempts: attempt}, nil
			}
			last = verr
		}
		metrics.ObserveLLMAttempt("segment", provider, "invalid")
		prev = &llm.Repair{Output: raw, Errors: last.Fields}
	}
	return SegmentOutcome{}, fmt.Errorf("%w: %w", ErrInvalidOutput, last)
}

func parseAndValidate(raw json.RawMessage) (segment.Rule, *validation.Error) {
	rule, err := segment.Parse(raw)
	if err == nil {
		err = segment.Validate(rule)
	}
	if err == nil {
		return rule, nil
	}
	var ve *validation.Error
	if errors.As(err, &ve) {
		return segment.Rule{}, ve
	}
	// Parse/Validate only return *validation.Error; keep the contract even if that changes.
	return segment.Rule{}, &validation.Error{Fields: []validation.FieldError{{
		Path: "", Code: validation.CodeSchemaViolation, Message: "segment rule is invalid", Err: validation.ErrSchemaViolation,
	}}}
}

// clientErrorField maps the client errors that count as invalid output to their field error.
func clientErrorField(err error) (validation.FieldError, bool) {
	switch {
	case errors.Is(err, llm.ErrTruncated):
		return validation.FieldError{Path: "", Code: validation.CodeTruncatedOutput,
			Message: "the model output was cut off before the tool input was complete", Err: llm.ErrTruncated}, true
	case errors.Is(err, llm.ErrNoToolCall):
		return validation.FieldError{Path: "", Code: validation.CodeNoToolCall,
			Message: "the model answered without calling the tool", Err: llm.ErrNoToolCall}, true
	}
	return validation.FieldError{}, false
}
