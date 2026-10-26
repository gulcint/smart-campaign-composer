// Package secret provides a string type whose every formatting path is masked,
// so API keys and database URLs cannot leak through logs or JSON (REQ-AI-04).
package secret

import (
	"encoding/json"
	"log/slog"
	"strings"
)

// Secret holds a sensitive string (API key, DB URL with password).
// Every formatting path is masked; only Reveal returns the raw value.
type Secret string

// Mask replaces a set secret in every rendered form.
const Mask = "[REDACTED]"

// Reveal returns the raw value. Call it only where the value is handed to a client library.
func (s Secret) Reveal() string { return string(s) }

// IsSet reports whether the secret holds a non-blank value.
func (s Secret) IsSet() bool { return strings.TrimSpace(string(s)) != "" }

// String returns Mask if the secret is set, "" otherwise.
func (s Secret) String() string {
	if s.IsSet() {
		return Mask
	}
	return ""
}

// GoString is the same as String (%#v).
func (s Secret) GoString() string { return s.String() }

// LogValue masks the secret in slog output.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalJSON encodes the masked form.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// MarshalText encodes the masked form.
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }
