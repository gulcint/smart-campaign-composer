// Package content holds the campaign copy model and its limits.
//
// P2 provides the types, limits and the length/name helpers the seed needs;
// Parse, Validate, Render and the worst-case checks arrive with the content
// slice (P4, docs/contracts/go-interfaces.md §content).
package content

import (
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	SMSMaxRunes       = 160
	SubjectMaxRunes   = 80
	EmailBodyMaxRunes = 5000
	FirstNameFallback = "Değerli müşterimiz"
)

// AllowedVariables are the only template placeholders (REQ-CNT-03).
var AllowedVariables = []string{"first_name", "last_name", "city"}

// Content is one SMS + email copy (content.schema.json).
type Content struct {
	SMS   SMS   `json:"sms"`
	Email Email `json:"email"`
}

// SMS is the SMS part of a Content.
type SMS struct {
	Body string `json:"body"`
}

// Email is the email part of a Content.
type Email struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// WorstCase are the post-substitution lengths reported on every content version whose template
// syntax is valid (API field ContentVersion.worst_case).
type WorstCase struct {
	SMSRunes     int `json:"sms_runes"`
	SubjectRunes int `json:"subject_runes"`
}

// Longest holds the longest value lengths (runes) over the customers MATCHED by the segment rule at
// its reference time (before consent filtering). FirstName already includes the fallback.
type Longest struct {
	FirstName int `json:"first_name"`
	LastName  int `json:"last_name"`
	City      int `json:"city"`
}

// RuneCount returns the number of Unicode code points of the NFC form of s.
func RuneCount(s string) int {
	return utf8.RuneCountInString(norm.NFC.String(s))
}

// EffectiveFirstName returns strings.TrimSpace(s), or FirstNameFallback if that is empty.
func EffectiveFirstName(s string) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	return FirstNameFallback
}

// Field errors of the content validator match these sentinels (empty fields match validation.ErrRequired).
var (
	ErrSMSTooLong        = errors.New("content: sms too long")        // "sms_too_long", "sms_too_long_worst_case"
	ErrSubjectTooLong    = errors.New("content: subject too long")    // "subject_too_long", "subject_too_long_worst_case"
	ErrEmailBodyTooLong  = errors.New("content: email body too long") // "email_body_too_long"
	ErrUnknownVariable   = errors.New("content: unknown variable")    // "unknown_variable"
	ErrMalformedVariable = errors.New("content: malformed variable")  // "malformed_variable"
	ErrWorstCaseTooLong  = errors.New("content: worst case too long") // additionally matched by *_worst_case codes
)
