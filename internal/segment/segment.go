// Package segment is the audience rule DSL (docs/contracts/segment-dsl.md).
//
// P2 only provides the Audience result type used by the campaign model; Parse,
// Validate, Compile and Match arrive with the audience slice (P3).
package segment

import "errors"

// Audience is the matched set size and its per-channel, consent-filtered subsets (REQ-AUD-03).
type Audience struct {
	Matched int `json:"matched"`
	SMS     int `json:"sms"`
	Email   int `json:"email"`
}

// Field errors of Validate match these sentinels ("invalid_value" matches validation.ErrInvalidValue).
var (
	ErrUnknownField          = errors.New("segment: unknown field")           // "unknown_field"
	ErrUnknownOperator       = errors.New("segment: unknown operator")        // "unknown_operator"
	ErrOperatorNotAllowed    = errors.New("segment: operator not allowed")    // "operator_not_allowed"
	ErrTypeMismatch          = errors.New("segment: type mismatch")           // "type_mismatch"
	ErrEmptyGroup            = errors.New("segment: empty group")             // "empty_group"
	ErrMaxDepthExceeded      = errors.New("segment: max depth exceeded")      // "max_depth_exceeded"
	ErrMaxConditionsExceeded = errors.New("segment: max conditions exceeded") // "max_conditions_exceeded"
)
