package profile

import (
	"math"
	"sync"
	"time"
)

const (
	// FrameTimelineSampleLimit bounds the retained samples per duration series.
	FrameTimelineSampleLimit = 512
	// FrameTimelineSchemaVersion versions the public gdk_pipeline object.
	FrameTimelineSchemaVersion = 1
	frameTimelineSeriesCount   = 4
)

// FrameTimelineSeries identifies one bounded duration series.
type FrameTimelineSeries string

// Duration series reported by the GDK present pipeline.
const (
	SeriesReceivedToImport FrameTimelineSeries = "received_to_import_ms"
	SeriesQueueWait        FrameTimelineSeries = "queue_wait_ms"
	SeriesImportElapsed    FrameTimelineSeries = "import_elapsed_ms"
	SeriesReceivedToSwap   FrameTimelineSeries = "received_to_swap_ms"
)

// frameTimelineSeriesOrder fixes the report order of the four series.
var frameTimelineSeriesOrder = [frameTimelineSeriesCount]FrameTimelineSeries{
	SeriesReceivedToImport,
	SeriesQueueWait,
	SeriesImportElapsed,
	SeriesReceivedToSwap,
}

func seriesIndex(series FrameTimelineSeries) int {
	for index, candidate := range frameTimelineSeriesOrder {
		if candidate == series {
			return index
		}
	}
	return -1
}

// durationRing is a fixed-capacity ring that remembers the newest samples and
// counts how many older samples it discarded. It is not safe for concurrent use.
type durationRing struct {
	values      [FrameTimelineSampleLimit]float64
	start       int
	count       int
	total       uint64
	overwritten uint64
}

func (r *durationRing) add(milliseconds float64) {
	if math.IsNaN(milliseconds) {
		return
	}
	if r.count == FrameTimelineSampleLimit {
		r.values[r.start] = milliseconds
		r.start = (r.start + 1) % FrameTimelineSampleLimit
		r.overwritten++
	} else {
		r.values[(r.start+r.count)%FrameTimelineSampleLimit] = milliseconds
		r.count++
	}
	r.total++
}

// snapshot copies the retained samples in arrival order.
func (r *durationRing) snapshot() (values []float64, total, overwritten uint64) {
	values = make([]float64, r.count)
	for offset := range r.count {
		values[offset] = r.values[(r.start+offset)%FrameTimelineSampleLimit]
	}
	return values, r.total, r.overwritten
}

// FrameTimelineSeriesSnapshot is a bounded copy of one duration series.
type FrameTimelineSeriesSnapshot struct {
	Series      FrameTimelineSeries
	Values      []float64
	Total       uint64
	Overwritten uint64
}

// FrameTimelineSnapshot is a bounded copy of one profiling window.
//
// Values are wall-clock elapsed durations measured on the caller's monotonic
// clock; they are not CPU execution times.
type FrameTimelineSnapshot struct {
	Received               uint64
	Replaced               uint64
	Imported               uint64
	Swapped                uint64
	PaintCycles            uint64
	OverwrittenBeforePaint uint64
	FeedbackAvailable      uint64
	FeedbackUnavailable    uint64
	Series                 [frameTimelineSeriesCount]FrameTimelineSeriesSnapshot
}

// SeriesFor returns the bounded copy for one duration series.
func (s FrameTimelineSnapshot) SeriesFor(series FrameTimelineSeries) FrameTimelineSeriesSnapshot {
	index := seriesIndex(series)
	if index < 0 {
		return FrameTimelineSeriesSnapshot{Series: series}
	}
	return s.Series[index]
}

// Quantiles returns the nearest-rank p50/p95/p99 values and the retained sample
// count for one series. Quantiles are computed over the retained samples only.
func (s FrameTimelineSnapshot) Quantiles(series FrameTimelineSeries) (p50, p95, p99 float64, count int) {
	index := seriesIndex(series)
	if index < 0 {
		return 0, 0, 0, 0
	}
	values := s.Series[index].Values
	if len(values) == 0 {
		return 0, 0, 0, 0
	}
	ordered := make([]float64, len(values))
	copy(ordered, values)
	sortFloat64s(ordered)
	return nearestRank(ordered, 0.50), nearestRank(ordered, 0.95), nearestRank(ordered, 0.99), len(ordered)
}

func nearestRank(sorted []float64, quantile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(quantile * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func sortFloat64s(values []float64) {
	// Insertion sort keeps this dependency-free and is fast for the bounded
	// sample counts here; the input is at most FrameTimelineSampleLimit long.
	for i := 1; i < len(values); i++ {
		current := values[i]
		j := i - 1
		for j >= 0 && values[j] > current {
			values[j+1] = values[j]
			j--
		}
		values[j+1] = current
	}
}

// FrameTimeline aggregates the GDK present pipeline for one profiling window.
//
// A nil FrameTimeline is inert, so disabled profiling allocates no rings and
// records nothing. All methods are safe for concurrent use.
type FrameTimeline struct {
	mu                     sync.Mutex
	rings                  [frameTimelineSeriesCount]durationRing
	received               uint64
	replaced               uint64
	imported               uint64
	swapped                uint64
	paintCycles            uint64
	overwrittenBeforePaint uint64
	feedbackAvailable      uint64
	feedbackUnavailable    uint64
}

// NewFrameTimeline returns an empty timeline.
func NewFrameTimeline() *FrameTimeline { return &FrameTimeline{} }

// ObserveFrameReceived counts one accelerated frame accepted for import.
func (t *FrameTimeline) ObserveFrameReceived() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.received++
	t.mu.Unlock()
}

// ObserveFrameReplaced counts one accepted frame that superseded a pending one.
func (t *FrameTimeline) ObserveFrameReplaced() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.replaced++
	t.mu.Unlock()
}

// ObserveFrameImported records one successfully imported frame and its elapsed
// durations. All four durations must come from the same monotonic clock; an
// observation carrying a negative elapsed time is rejected outright rather than
// clamped or partially recorded.
func (t *FrameTimeline) ObserveFrameImported(receivedToImport, queueWait, importElapsed, receivedToSwap time.Duration) {
	if t == nil {
		return
	}
	if receivedToImport < 0 || queueWait < 0 || importElapsed < 0 || receivedToSwap < 0 {
		return
	}
	t.mu.Lock()
	t.imported++
	t.swapped++
	t.rings[seriesIndex(SeriesReceivedToImport)].add(durationMS(receivedToImport))
	t.rings[seriesIndex(SeriesQueueWait)].add(durationMS(queueWait))
	t.rings[seriesIndex(SeriesImportElapsed)].add(durationMS(importElapsed))
	t.rings[seriesIndex(SeriesReceivedToSwap)].add(durationMS(receivedToSwap))
	t.mu.Unlock()
}

// ObserveImportWithoutSwap records an import that never reached the presenter.
// Failed imports are counted but contribute no swap duration sample.
func (t *FrameTimeline) ObserveImportWithoutSwap() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.imported++
	t.mu.Unlock()
}

// ObservePaintCycle records one GTK frame-clock paint cycle association.
func (t *FrameTimeline) ObservePaintCycle() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.paintCycles++
	t.mu.Unlock()
}

// ObserveSwapsOverwrittenBeforePaint records swaps that were superseded before
// a paint cycle observed them. It is an upper bound on missed opportunities,
// not proof of a dropped scanout.
func (t *FrameTimeline) ObserveSwapsOverwrittenBeforePaint(count uint64) {
	if t == nil || count == 0 {
		return
	}
	t.mu.Lock()
	t.overwrittenBeforePaint += count
	t.mu.Unlock()
}

// ObserveFrameFeedback records whether a paint cycle's surface feedback was
// available. Missing feedback is unavailable, never zero latency.
func (t *FrameTimeline) ObserveFrameFeedback(available bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if available {
		t.feedbackAvailable++
	} else {
		t.feedbackUnavailable++
	}
	t.mu.Unlock()
}

// Snapshot copies bounded data under the timeline lock. Quantiles are computed
// by the caller, outside renderer and lifecycle locks.
func (t *FrameTimeline) Snapshot() FrameTimelineSnapshot {
	if t == nil {
		return FrameTimelineSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked()
}

func (t *FrameTimeline) snapshotLocked() FrameTimelineSnapshot {
	snapshot := FrameTimelineSnapshot{
		Received:               t.received,
		Replaced:               t.replaced,
		Imported:               t.imported,
		Swapped:                t.swapped,
		PaintCycles:            t.paintCycles,
		OverwrittenBeforePaint: t.overwrittenBeforePaint,
		FeedbackAvailable:      t.feedbackAvailable,
		FeedbackUnavailable:    t.feedbackUnavailable,
	}
	for index, series := range frameTimelineSeriesOrder {
		values, total, overwritten := t.rings[index].snapshot()
		snapshot.Series[index] = FrameTimelineSeriesSnapshot{
			Series:      series,
			Values:      values,
			Total:       total,
			Overwritten: overwritten,
		}
	}
	return snapshot
}

// Drain copies bounded data and resets the window in one lock acquisition, so no
// sample is lost between reading and clearing.
func (t *FrameTimeline) Drain() FrameTimelineSnapshot {
	if t == nil {
		return FrameTimelineSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	snapshot := t.snapshotLocked()
	t.rings = [frameTimelineSeriesCount]durationRing{}
	t.received = 0
	t.replaced = 0
	t.imported = 0
	t.swapped = 0
	t.paintCycles = 0
	t.overwrittenBeforePaint = 0
	t.feedbackAvailable = 0
	t.feedbackUnavailable = 0
	return snapshot
}

// Reset returns the timeline to its initial state, discarding all samples.
func (t *FrameTimeline) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.rings = [frameTimelineSeriesCount]durationRing{}
	t.received = 0
	t.replaced = 0
	t.imported = 0
	t.swapped = 0
	t.paintCycles = 0
	t.overwrittenBeforePaint = 0
	t.feedbackAvailable = 0
	t.feedbackUnavailable = 0
	t.mu.Unlock()
}
