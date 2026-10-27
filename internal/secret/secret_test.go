package secret_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gulcint/smart-campaign-composer/internal/secret"
)

// raw looks like an Anthropic key but is too short for the gitleaks rule; it is never valid.
const raw = "sk-ant-test-0123"

// REQ-AI-03
// A present key is detected as set, an absent (empty) one is not ("key varsa/yoksa").
func TestSecret_RevealAndIsSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		in     secret.Secret
		wantOK bool
	}{
		{name: "non-empty value is set", in: secret.Secret(raw), wantOK: true},
		{name: "empty value is not set", in: secret.Secret(""), wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.IsSet(); got != tc.wantOK {
				t.Fatalf("IsSet() = %v, want %v", got, tc.wantOK)
			}
			if got := tc.in.Reveal(); got != string(tc.in) {
				t.Fatalf("Reveal() = %q, want the raw value", got)
			}
		})
	}
}

// REQ-AI-04
// Every formatting path masks the value; an unset secret renders as "".
func TestSecret_FormattingIsMasked(t *testing.T) {
	t.Parallel()
	s := secret.Secret(raw)
	render := []struct {
		name string
		out  func() (string, error)
		want string // "" => only check that raw is absent
	}{
		{name: "String", out: func() (string, error) { return s.String(), nil }, want: secret.Mask},
		{name: "GoString", out: func() (string, error) { return s.GoString(), nil }, want: secret.Mask},
		{name: "%v", out: func() (string, error) { return fmt.Sprintf("%v", s), nil }, want: secret.Mask},
		{name: "%s", out: func() (string, error) { return fmt.Sprintf("%s", s), nil }, want: secret.Mask},
		{name: "%+v", out: func() (string, error) { return fmt.Sprintf("%+v", s), nil }, want: secret.Mask},
		{name: "%#v", out: func() (string, error) { return fmt.Sprintf("%#v", s), nil }, want: secret.Mask},
		{name: "%q", out: func() (string, error) { return fmt.Sprintf("%q", s), nil }},
		{name: "%x", out: func() (string, error) { return fmt.Sprintf("%x", s), nil }},
		{name: "Sprint", out: func() (string, error) { return fmt.Sprint(s), nil }, want: secret.Mask},
		{name: "inside a struct with %+v", out: func() (string, error) {
			return fmt.Sprintf("%+v", struct{ Key secret.Secret }{s}), nil
		}},
		{name: "inside a struct with %#v", out: func() (string, error) {
			return fmt.Sprintf("%#v", struct{ Key secret.Secret }{s}), nil
		}},
		{name: "inside a slice", out: func() (string, error) { return fmt.Sprint([]secret.Secret{s}), nil }},
		{name: "wrapped in an error", out: func() (string, error) { return fmt.Errorf("connect: %v", s).Error(), nil }},
		{name: "MarshalText", out: func() (string, error) { b, err := s.MarshalText(); return string(b), err }, want: secret.Mask},
		{name: "MarshalJSON", out: func() (string, error) { b, err := s.MarshalJSON(); return string(b), err }, want: `"` + secret.Mask + `"`},
		{name: "json.Marshal in a struct", out: func() (string, error) {
			b, err := json.Marshal(struct {
				Key secret.Secret `json:"key"`
			}{s})
			return string(b), err
		}, want: `{"key":"` + secret.Mask + `"}`},
		{name: "slog JSON handler", out: func() (string, error) {
			var buf bytes.Buffer
			slog.New(slog.NewJSONHandler(&buf, nil)).Info("cfg", "api_key", s)
			return buf.String(), nil
		}},
		{name: "slog text handler with slog.Any", out: func() (string, error) {
			var buf bytes.Buffer
			slog.New(slog.NewTextHandler(&buf, nil)).Info("cfg", slog.Any("api_key", s))
			return buf.String(), nil
		}},
		{name: "LogValue", out: func() (string, error) { return s.LogValue().String(), nil }, want: secret.Mask},
	}
	for _, tc := range render {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.out()
			if err != nil {
				t.Fatalf("render error = %v", err)
			}
			if strings.Contains(got, raw) || strings.Contains(got, hex.EncodeToString([]byte(raw))) {
				t.Fatalf("output contains the raw secret: %q", got)
			}
			if tc.want != "" && got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
		})
	}
}
