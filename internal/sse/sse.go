// Package sse writes Server-Sent Events (sse-events.md).
//
// P2 only defines the contract's sentinel error; the writer arrives with the content slice (P4).
package sse

import "errors"

var ErrStreamingUnsupported = errors.New("sse: response writer cannot flush")
