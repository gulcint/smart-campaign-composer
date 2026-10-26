// Package provider is the HTTP client of the SMS/email provider (mock-provider.md).
//
// P2 only defines the contract's sentinel errors; the client arrives with the delivery slice (P5).
package provider

import "errors"

var (
	ErrTimeout    = errors.New("provider: timeout")
	ErrConnection = errors.New("provider: connection error")
)
