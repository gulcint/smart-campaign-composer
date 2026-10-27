package composer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/metrics"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// DeltaEvent is one streamed text chunk of one field (SSE event "delta").
type DeltaEvent struct {
	Attempt int    `json:"attempt"`
	Channel string `json:"channel"` // "sms" | "email"
	Field   string `json:"field"`   // "body" | "subject"
	Text    string `json:"text"`
}

// InvalidEvent reports a failed attempt (SSE event "invalid"). ContentVersionID is always encoded,
// as null when no draft was stored.
type InvalidEvent struct {
	Attempt          int                     `json:"attempt"`
	Final            bool                    `json:"final"`
	Errors           []validation.FieldError `json:"errors"`
	ContentVersionID *string                 `json:"content_version_id"`
}

// ValidatedEvent is the terminal event of a successful generation (SSE event "validated").
type ValidatedEvent struct {
	Attempt          int               `json:"attempt"`
	ContentVersionID string            `json:"content_version_id"`
	VersionNumber    int               `json:"version_number"`
	Content          content.Content   `json:"content"`
	WorstCase        content.WorstCase `json:"worst_case"`
}

func (DeltaEvent) Name() string     { return "delta" }
func (InvalidEvent) Name() string   { return "invalid" }
func (ValidatedEvent) Name() string { return "validated" }

// ContentOutcome is the result of a content generation, before persistence.
type ContentOutcome struct {
	Valid     bool
	Content   content.Content         // set when Valid or Storable
	WorstCase *content.WorstCase      // set when the template syntax is valid
	Errors    []validation.FieldError // last attempt's errors when !Valid
	// Storable: !Valid, content.Parse succeeded and every error is a content-rule error => the
	// draft is stored as status "invalid".
	Storable bool
	Attempts int
}

// ContentSession is an opened generation; attempt 1's stream is already connected.
type ContentSession struct {
	ctx      context.Context
	client   llm.Client
	req      llm.ContentRequest
	stream   llm.ContentStream
	provider string
}

// OpenContent starts attempt 1. req.Longest is used both as model hint and for content.Validate.
// Errors: llm.ErrUnavailable, ctx errors.
func OpenContent(ctx context.Context, c llm.Client, req llm.ContentRequest) (*ContentSession, error) {
	req.Attempt, req.Previous = 1, nil
	s := &ContentSession{ctx: ctx, client: c, req: req, provider: string(llm.ProviderOf(c))}
	st, err := c.StreamContent(ctx, req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		metrics.ObserveLLMAttempt("content", s.provider, "unavailable")
		return nil, asUnavailable(err)
	}
	s.stream = st
	return s, nil
}

func asUnavailable(err error) error {
	if errors.Is(err, llm.ErrUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %w", llm.ErrUnavailable, err)
}

// contentRuleCodes are the codes of a structurally valid draft that breaks the content rules.
var contentRuleCodes = []string{
	validation.CodeRequired, validation.CodeMalformedVariable, validation.CodeUnknownVariable,
	validation.CodeSMSTooLong, validation.CodeSubjectTooLong, validation.CodeEmailBodyTooLong,
}

// Run consumes the stream(s), emitting every DeltaEvent and the non-final InvalidEvent of a failed
// attempt 1, then performs the single repair attempt if needed. Validation = content.Parse +
// content.Validate(c, req.Longest), the same as the edit path. llm.ErrUnavailable after the stream
// opened ends the generation without repair (code "llm_unavailable"). Run never emits the terminal
// event. It returns ctx errors on cancellation and the error returned by emit.
func (s *ContentSession) Run(emit func(Event) error) (ContentOutcome, error) {
	stream := s.stream
	var prev *llm.Repair
	for attempt := 1; ; attempt++ {
		if attempt > 1 {
			metrics.ObserveLLMRepair("content")
			req := s.req
			req.Attempt, req.Previous = attempt, prev
			st, err := s.client.StreamContent(s.ctx, req)
			if err != nil {
				if ctxErr := s.ctx.Err(); ctxErr != nil {
					return ContentOutcome{}, ctxErr
				}
				metrics.ObserveLLMAttempt("content", s.provider, "unavailable")
				return unavailableOutcome(attempt), nil
			}
			stream = st
		}

		raw, endField, unavailable, err := s.consume(stream, attempt, emit)
		_ = stream.Close()
		if err != nil {
			return ContentOutcome{}, err
		}
		if unavailable {
			metrics.ObserveLLMAttempt("content", s.provider, "unavailable")
			return unavailableOutcome(attempt), nil
		}

		var out ContentOutcome
		if endField != nil {
			out.Errors = []validation.FieldError{*endField}
		} else {
			out = s.evaluate(raw)
		}
		out.Attempts = attempt
		if out.Valid {
			metrics.ObserveLLMAttempt("content", s.provider, "valid")
			return out, nil
		}
		metrics.ObserveLLMAttempt("content", s.provider, "invalid")
		if attempt >= maxAttempts {
			return out, nil
		}
		if err := emit(InvalidEvent{Attempt: attempt, Final: false, Errors: out.Errors}); err != nil {
			return ContentOutcome{}, err
		}
		prev = &llm.Repair{Output: raw, Errors: out.Errors}
	}
}

func unavailableOutcome(attempt int) ContentOutcome {
	return ContentOutcome{Attempts: attempt, Errors: []validation.FieldError{{
		Path: "", Code: validation.CodeLLMUnavailable, Message: "the language model became unavailable during the generation",
	}}}
}

// consume reads one attempt's stream, emitting a DeltaEvent per chunk. It returns the raw tool
// input (complete on io.EOF, partial otherwise), the field error of an invalid ending
// (truncated_output, no_tool_call), whether the model became unavailable, or a ctx / emit error.
func (s *ContentSession) consume(stream llm.ContentStream, attempt int, emit func(Event) error) (json.RawMessage, *validation.FieldError, bool, error) {
	for {
		chunk, err := stream.Recv()
		if err == nil {
			channel, field, ok := splitPath(chunk.Path)
			if !ok || chunk.Text == "" {
				continue
			}
			if err := emit(DeltaEvent{Attempt: attempt, Channel: channel, Field: field, Text: chunk.Text}); err != nil {
				return nil, nil, false, err
			}
			continue
		}
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return nil, nil, false, ctxErr
		}
		switch {
		case errors.Is(err, io.EOF):
			return stream.Raw(), nil, false, nil
		case errors.Is(err, llm.ErrTruncated), errors.Is(err, llm.ErrNoToolCall):
			fe, _ := clientErrorField(err)
			return stream.Raw(), &fe, false, nil
		default: // llm.ErrUnavailable or any other transport failure
			return nil, nil, true, nil
		}
	}
}

func splitPath(path string) (channel, field string, ok bool) {
	switch path {
	case "sms.body":
		return "sms", "body", true
	case "email.subject":
		return "email", "subject", true
	case "email.body":
		return "email", "body", true
	}
	return "", "", false
}

// evaluate validates one complete tool input exactly like an edit.
func (s *ContentSession) evaluate(raw json.RawMessage) ContentOutcome {
	c, err := content.Parse(raw)
	if err != nil {
		return ContentOutcome{Errors: fieldErrors(err)}
	}
	out := ContentOutcome{Content: c}
	if wc, err := content.MeasureWorstCase(c, s.req.Longest); err == nil {
		out.WorstCase = &wc
	}
	verr := content.Validate(c, s.req.Longest)
	if verr == nil {
		out.Valid = true
		return out
	}
	out.Errors = fieldErrors(verr)
	out.Storable = true
	for _, f := range out.Errors {
		if !slices.Contains(contentRuleCodes, f.Code) {
			out.Storable = false
		}
	}
	return out
}

func fieldErrors(err error) []validation.FieldError {
	var ve *validation.Error
	if errors.As(err, &ve) && len(ve.Fields) > 0 {
		return ve.Fields
	}
	return []validation.FieldError{{Path: "", Code: validation.CodeSchemaViolation, Message: "content is invalid", Err: validation.ErrSchemaViolation}}
}
