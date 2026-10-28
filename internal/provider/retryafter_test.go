package provider_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/provider"
)

// t0 is the test reference time of docs/requirements.md.
var t0 = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// REQ-SND-04
// provider.ParseRetryAfter reads seconds ("2") or an HTTP date relative to now; a past date is a
// 0 s wait; an empty or unparsable value is (0, false), i.e. treated as absent (backoff).
func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		value  string
		want   time.Duration
		wantOK bool
	}{
		{name: "seconds", value: "2", want: 2 * time.Second, wantOK: true},
		{name: "one second", value: "1", want: time.Second, wantOK: true},
		{name: "zero seconds", value: "0", want: 0, wantOK: true},
		{name: "two minutes in seconds", value: "120", want: 120 * time.Second, wantOK: true},
		{name: "HTTP date 5 s ahead", value: t0.Add(5 * time.Second).Format(http.TimeFormat), want: 5 * time.Second, wantOK: true},
		{name: "HTTP date 10 min ahead", value: t0.Add(10 * time.Minute).Format(http.TimeFormat), want: 10 * time.Minute, wantOK: true},
		{name: "HTTP date equal to now", value: t0.Format(http.TimeFormat), want: 0, wantOK: true},
		{name: "past HTTP date is 0", value: t0.Add(-30 * time.Second).Format(http.TimeFormat), want: 0, wantOK: true},
		{name: "long past HTTP date is 0", value: "Thu, 01 Jan 2026 00:00:00 GMT", want: 0, wantOK: true},
		{name: "empty is absent", value: "", want: 0, wantOK: false},
		{name: "word is unparsable", value: "soon", want: 0, wantOK: false},
		{name: "garbled date is unparsable", value: "Thu, 99 Foo 2026 25:61:00 GMT", want: 0, wantOK: false},
		{name: "seconds with a unit are unparsable", value: "2s", want: 0, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := provider.ParseRetryAfter(tc.value, t0)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("ParseRetryAfter(%q, t0) = (%v, %v), want (%v, %v)", tc.value, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
