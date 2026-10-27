//go:build race

package segment_test

// raceSlowdown scales wall-clock bounds of the cost tests; the race detector slows code down.
const raceSlowdown = 8
