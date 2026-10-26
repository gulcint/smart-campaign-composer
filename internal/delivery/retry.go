// Package delivery is the send queue: retry policy, rate limiter and dispatcher.
//
// P2 only provides RetryConfig, which the configuration loader fills from the
// environment; the policy, limiter and dispatcher arrive with the delivery slice (P5).
package delivery

import "time"

// RetryConfig bounds retries per delivery (REQ-SND-04, PLAN §0.4).
type RetryConfig struct {
	MaxAttempts     int           // 5: failed attempts incl. the first; the 5th failure => failed
	BaseDelay       time.Duration // 1s
	MaxDelay        time.Duration // 60s
	MaxRateLimited  int           // 20: the 20th 429 => failed
	RateLimitWindow time.Duration // 10m, measured from FirstAttemptAt
}

// DefaultRetryConfig returns the documented defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:     5,
		BaseDelay:       time.Second,
		MaxDelay:        60 * time.Second,
		MaxRateLimited:  20,
		RateLimitWindow: 10 * time.Minute,
	}
}
