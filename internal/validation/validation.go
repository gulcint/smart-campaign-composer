// Package validation holds the field-error model shared by every validator
// (segment rules, content, briefs, request bodies) and its closed list of codes.
package validation

import (
	"errors"
	"strings"
)

// FieldError is one problem at one path of a document.
type FieldError struct {
	Path    string `json:"path"`             // e.g. "sms.body", "rule.all[0].field", "brief", "" (whole document)
	Code    string `json:"code"`             // closed list, contracts README §5.2
	Message string `json:"message"`          // English, human readable, never contains secrets
	Limit   *int   `json:"limit,omitempty"`  // set for length codes
	Actual  *int   `json:"actual,omitempty"` // set for length codes (rune count)
	Err     error  `json:"-"`                // sentinel this field error matches
}

// Error aggregates field errors. errors.Is(err, X) is true if any FieldError.Err matches X.
type Error struct{ Fields []FieldError }

func (e *Error) Error() string {
	if len(e.Fields) == 0 {
		return "validation failed"
	}
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		p := f.Path
		if p == "" {
			p = "(document)"
		}
		parts = append(parts, p+": "+f.Code)
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// Unwrap exposes every field's sentinel to errors.Is / errors.As.
func (e *Error) Unwrap() []error {
	errs := make([]error, 0, len(e.Fields))
	for _, f := range e.Fields {
		if f.Err != nil {
			errs = append(errs, f.Err)
		}
	}
	return errs
}

var (
	ErrInvalidJSON     = errors.New("validation: invalid json")     // code "invalid_json"
	ErrSchemaViolation = errors.New("validation: schema violation") // code "schema_violation"
	ErrRequired        = errors.New("validation: required")         // code "required"
	ErrTooLong         = errors.New("validation: too long")         // code "too_long"
	ErrInvalidValue    = errors.New("validation: invalid value")    // code "invalid_value"
)

// Code constants (string values are the wire codes).
const (
	CodeRequired                = "required"
	CodeTooLong                 = "too_long"
	CodeInvalidValue            = "invalid_value"
	CodeInvalidJSON             = "invalid_json"
	CodeSchemaViolation         = "schema_violation"
	CodeTruncatedOutput         = "truncated_output"
	CodeNoToolCall              = "no_tool_call"
	CodeLLMUnavailable          = "llm_unavailable"
	CodeInternalError           = "internal_error"
	CodeSMSTooLong              = "sms_too_long"
	CodeSubjectTooLong          = "subject_too_long"
	CodeEmailBodyTooLong        = "email_body_too_long"
	CodeSMSTooLongWorstCase     = "sms_too_long_worst_case"
	CodeSubjectTooLongWorstCase = "subject_too_long_worst_case"
	CodeUnknownVariable         = "unknown_variable"
	CodeMalformedVariable       = "malformed_variable"
	CodeUnknownField            = "unknown_field"
	CodeUnknownOperator         = "unknown_operator"
	CodeOperatorNotAllowed      = "operator_not_allowed"
	CodeTypeMismatch            = "type_mismatch"
	CodeEmptyGroup              = "empty_group"
	CodeMaxDepthExceeded        = "max_depth_exceeded"
	CodeMaxConditionsExceeded   = "max_conditions_exceeded"
	CodeScheduleTooSoon         = "schedule_too_soon" // P7a, path "scheduled_at"
	CodeScheduleTooFar          = "schedule_too_far"  // P7a, path "scheduled_at"
)
