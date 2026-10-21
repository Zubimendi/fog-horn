package histogram_test

import (
	"math"
	"testing"

	"github.com/zubimendi/fog-horn/internal/rollup/histogram"
)

func TestObserveAndCount(t *testing.T) {
	var h histogram.Histogram
	h.Observe(10.0)
	h.Observe(50.0)
	h.Observe(500.0)
	if h.Count != 3 {
		t.Errorf("Count = %d, want 3", h.Count)
	}
	if math.Abs(h.SumMS-560.0) > 0.001 {
		t.Errorf("SumMS = %v, want 560.0", h.SumMS)
	}
}

// TestMergeEqualsRawData: merging two hours' buckets must equal the
// bucket counts of the combined raw data.
func TestMergeEqualsRawData(t *testing.T) {
	// Build raw combined histogram from all observations
	var raw histogram.Histogram
	observations := []float64{5, 10, 50, 100, 250, 500, 1000, 5000}
	for _, v := range observations {
		raw.Observe(v)
	}

	// Split into two histograms and merge
	var h1, h2 histogram.Histogram
	for i, v := range observations {
		if i%2 == 0 {
			h1.Observe(v)
		} else {
			h2.Observe(v)
		}
	}
	merged := histogram.Merge(h1, h2)

	// Verify bucket-by-bucket equality
	for i := range raw.Counts {
		if merged.Counts[i] != raw.Counts[i] {
			t.Errorf("bucket[%d]: merged=%d, raw=%d", i, merged.Counts[i], raw.Counts[i])
		}
	}
	if merged.Count != raw.Count {
		t.Errorf("Count: merged=%d, raw=%d", merged.Count, raw.Count)
	}
	if math.Abs(merged.SumMS-raw.SumMS) > 0.001 {
		t.Errorf("SumMS: merged=%v, raw=%v", merged.SumMS, raw.SumMS)
	}
}

func TestPercentileFromMergedWithinBucketResolution(t *testing.T) {
	// Insert 100 observations at 50ms
	var h histogram.Histogram
	for i := 0; i < 100; i++ {
		h.Observe(50.0)
	}
	p50 := histogram.Percentile(h, 0.5)
	// 50ms falls in the bucket [25, 50], so p50 must be in that range
	if p50 < 25.0 || p50 > 50.0 {
		t.Errorf("p50 = %v, expected within bucket [25, 50]", p50)
	}
}

func TestPercentileP99(t *testing.T) {
	// 99 observations at 10ms, 1 at 5000ms
	var h histogram.Histogram
	for i := 0; i < 99; i++ {
		h.Observe(10.0)
	}
	h.Observe(5000.0)
	p99 := histogram.Percentile(h, 0.99)
	// p99 should be in the 10ms bucket (99th observation out of 100)
	if p99 > 25.0 {
		t.Errorf("p99 = %v, expected ≤ 25ms (99/100 are 10ms)", p99)
	}
}

func TestMergeIsCommutative(t *testing.T) {
	var a, b histogram.Histogram
	a.Observe(10)
	a.Observe(100)
	b.Observe(1000)
	b.Observe(5)

	ab := histogram.Merge(a, b)
	ba := histogram.Merge(b, a)

	for i := range ab.Counts {
		if ab.Counts[i] != ba.Counts[i] {
			t.Errorf("Merge not commutative at bucket %d", i)
		}
	}
}

func TestMergeIsAssociative(t *testing.T) {
	var a, b, c histogram.Histogram
	a.Observe(5)
	b.Observe(50)
	c.Observe(500)

	abc := histogram.Merge(histogram.Merge(a, b), c)
	ab_c := histogram.Merge(a, histogram.Merge(b, c))

	for i := range abc.Counts {
		if abc.Counts[i] != ab_c.Counts[i] {
			t.Errorf("Merge not associative at bucket %d", i)
		}
	}
}

func TestFromCountsRoundTrip(t *testing.T) {
	var h histogram.Histogram
	h.Observe(10)
	h.Observe(500)
	h.Observe(5000)

	counts := h.ToCounts()
	h2 := histogram.FromCounts(counts, h.Count, h.SumMS)

	for i := range h.Counts {
		if h.Counts[i] != h2.Counts[i] {
			t.Errorf("round-trip failed at bucket %d: %d != %d", i, h.Counts[i], h2.Counts[i])
		}
	}
}

func TestMeanCalculation(t *testing.T) {
	var h histogram.Histogram
	h.Observe(100)
	h.Observe(200)
	h.Observe(300)
	mean := histogram.Mean(h)
	if math.Abs(mean-200.0) > 0.001 {
		t.Errorf("Mean = %v, want 200.0", mean)
	}
}

func TestZeroHistogram(t *testing.T) {
	var h histogram.Histogram
	if histogram.Percentile(h, 0.5) != 0 {
		t.Error("Percentile of empty histogram should be 0")
	}
	if histogram.Mean(h) != 0 {
		t.Error("Mean of empty histogram should be 0")
	}
}
