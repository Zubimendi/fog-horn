// Package histogram implements a fixed-bucket latency histogram.
// Histograms are mergeable by summing bucket counts; percentiles are computed
// from the merged distribution. Pure: no I/O.
package histogram

import (
	"math"
)

// Boundaries defines the upper bound of each histogram bucket in milliseconds.
// These match Prometheus default HTTP latency buckets scaled to ms.
var Boundaries = []float64{
	1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000,
	2500, 5000, 10000, 30000, 60000,
}

// NumBuckets is the number of buckets (includes +Inf implicitly as the last bucket: 15 boundaries + 1).
const NumBuckets = 16

// Histogram holds the fixed-bucket count distribution.
type Histogram struct {
	// Counts[i] is the count of observations in bucket i.
	// Bucket i covers (Boundaries[i-1], Boundaries[i]], with Bucket 0 = [0, Boundaries[0]].
	// The last bucket covers (Boundaries[len-1], +Inf).
	Counts [NumBuckets]int64
	Count  int64
	SumMS  float64
}

// Observe records a single observation in milliseconds.
func (h *Histogram) Observe(ms float64) {
	idx := NumBuckets - 1
	for i, b := range Boundaries {
		if ms <= b {
			idx = i
			break
		}
	}
	h.Counts[idx]++
	h.Count++
	h.SumMS += ms
}

// Merge returns a new histogram that is the sum of a and b.
// Histograms are additive: merged bucket counts equal combined raw data.
func Merge(a, b Histogram) Histogram {
	var result Histogram
	for i := range result.Counts {
		result.Counts[i] = a.Counts[i] + b.Counts[i]
	}
	result.Count = a.Count + b.Count
	result.SumMS = a.SumMS + b.SumMS
	return result
}

// Percentile estimates the p-th percentile (0 < p < 1) from the histogram
// using linear interpolation within the matching bucket.
// Resolution is limited by bucket width.
func Percentile(h Histogram, p float64) float64 {
	if h.Count == 0 || p <= 0 {
		return 0
	}
	if p >= 1 {
		return math.Inf(1)
	}

	target := int64(math.Ceil(p * float64(h.Count)))
	var cumulative int64

	for i, count := range h.Counts {
		cumulative += count
		if cumulative >= target {
			// Interpolate within bucket
			lo := 0.0
			if i > 0 {
				lo = Boundaries[i-1]
			}
			hi := math.Inf(1)
			if i < len(Boundaries) {
				hi = Boundaries[i]
			}
			if math.IsInf(hi, 1) {
				return lo
			}
			// Linear interpolation
			prevCumulative := cumulative - count
			fraction := float64(target-prevCumulative) / float64(count)
			return lo + fraction*(hi-lo)
		}
	}
	return math.Inf(1)
}

// Mean returns the arithmetic mean of all observations.
func Mean(h Histogram) float64 {
	if h.Count == 0 {
		return 0
	}
	return h.SumMS / float64(h.Count)
}

// FromCounts creates a histogram from a bucket count slice and sum.
// Used when loading rollup data from the database.
func FromCounts(counts []int64, count int64, sumMS float64) Histogram {
	var h Histogram
	n := len(counts)
	if n > NumBuckets {
		n = NumBuckets
	}
	copy(h.Counts[:n], counts)
	h.Count = count
	h.SumMS = sumMS
	return h
}

// ToCounts returns the bucket counts as a slice suitable for DB storage.
func (h Histogram) ToCounts() []int64 {
	out := make([]int64, NumBuckets)
	copy(out, h.Counts[:])
	return out
}
