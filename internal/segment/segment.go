// Package segment is the audience rule DSL (docs/contracts/segment-dsl.md).
//
// P2 only provides the Audience result type used by the campaign model; Parse,
// Validate, Compile and Match arrive with the audience slice (P3).
package segment

// Audience is the matched set size and its per-channel, consent-filtered subsets (REQ-AUD-03).
type Audience struct {
	Matched int `json:"matched"`
	SMS     int `json:"sms"`
	Email   int `json:"email"`
}
