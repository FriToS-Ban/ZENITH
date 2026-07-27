package metrics

import (
	"sort"
	"time"
)

// Percentiles computes p50, p95, and p99 from a slice of durations.
// The slice is sorted in place. Returns zeros if empty.
func Percentiles(samples []time.Duration) (p50, p95, p99 time.Duration) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	n := len(samples)
	p50 = samples[int(float64(n)*0.50)]
	p95 = samples[int(float64(n)*0.95)]
	p99 = samples[int(float64(n)*0.99)]
	return p50, p95, p99
}
