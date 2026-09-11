package profile

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFrameTimelineNilIsInert(t *testing.T) {
	var timeline *FrameTimeline
	timeline.ObserveFrameReceived()
	timeline.ObserveFrameReplaced()
	timeline.ObserveFrameImported(time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond)
	timeline.ObserveImportWithoutSwap()
	timeline.ObservePaintCycle()
	timeline.ObserveSwapsOverwrittenBeforePaint(3)
	timeline.ObserveFrameFeedback(true)
	timeline.Reset()

	snapshot := timeline.Snapshot()
	if snapshot.Received != 0 || snapshot.Swapped != 0 || snapshot.PaintCycles != 0 {
		t.Fatalf("nil timeline recorded data: %+v", snapshot)
	}
	if drained := timeline.Drain(); drained.Received != 0 {
		t.Fatalf("nil timeline drain = %+v", drained)
	}
}

func TestFrameTimelineCountsAndNearestRankQuantiles(t *testing.T) {
	timeline := NewFrameTimeline()
	for i := 1; i <= 100; i++ {
		timeline.ObserveFrameImported(
			time.Duration(i)*time.Millisecond,
			time.Duration(i)*time.Millisecond/2,
			time.Duration(i)*time.Millisecond/10,
			time.Duration(i)*2*time.Millisecond,
		)
	}
	timeline.ObserveFrameReceived()
	timeline.ObserveFrameReplaced()
	timeline.ObservePaintCycle()
	timeline.ObserveSwapsOverwrittenBeforePaint(2)
	timeline.ObserveFrameFeedback(true)
	timeline.ObserveFrameFeedback(false)

	snapshot := timeline.Snapshot()
	if snapshot.Received != 1 || snapshot.Replaced != 1 || snapshot.Imported != 100 || snapshot.Swapped != 100 {
		t.Fatalf("unexpected counters: %+v", snapshot)
	}
	if snapshot.PaintCycles != 1 || snapshot.OverwrittenBeforePaint != 2 {
		t.Fatalf("unexpected cycle counters: %+v", snapshot)
	}
	if snapshot.FeedbackAvailable != 1 || snapshot.FeedbackUnavailable != 1 {
		t.Fatalf("unexpected feedback counters: %+v", snapshot)
	}

	// Nearest rank over 100 samples: p50 -> 50, p95 -> 95, p99 -> 99.
	p50, p95, p99, count := snapshot.Quantiles(SeriesReceivedToImport)
	if count != 100 || p50 != 50 || p95 != 95 || p99 != 99 {
		t.Fatalf("received_to_import quantiles = (%v,%v,%v) count=%d, want (50,95,99) 100", p50, p95, p99, count)
	}
	p50, _, _, _ = snapshot.Quantiles(SeriesQueueWait)
	if p50 != 25 {
		t.Fatalf("queue_wait p50 = %v, want 25", p50)
	}
	p50, _, _, _ = snapshot.Quantiles(SeriesReceivedToSwap)
	if p50 != 100 {
		t.Fatalf("received_to_swap p50 = %v, want 100", p50)
	}
}

func TestFrameTimelineRingIsBoundedAndCountsOverwrites(t *testing.T) {
	timeline := NewFrameTimeline()
	const samples = FrameTimelineSampleLimit + 88
	for i := 0; i < samples; i++ {
		timeline.ObserveFrameImported(time.Duration(i+1)*time.Millisecond, 0, 0, 0)
	}
	snapshot := timeline.Snapshot()
	series := snapshot.Series[seriesIndex(SeriesReceivedToImport)]
	if series.Total != samples {
		t.Fatalf("total samples = %d, want %d", series.Total, samples)
	}
	if len(series.Values) != FrameTimelineSampleLimit {
		t.Fatalf("retained samples = %d, want %d", len(series.Values), FrameTimelineSampleLimit)
	}
	if series.Overwritten != 88 {
		t.Fatalf("overwritten samples = %d, want 88", series.Overwritten)
	}
	// The newest samples survive: the oldest retained value is 89 ms.
	if series.Values[0] != 89 {
		t.Fatalf("oldest retained sample = %v, want 89", series.Values[0])
	}
	if series.Values[len(series.Values)-1] != float64(samples) {
		t.Fatalf("newest retained sample = %v, want %d", series.Values[len(series.Values)-1], samples)
	}
	// Quantiles are computed over the retained samples only: 512 retained values
	// run from 89 to 600, so rank ceil(0.5*512)=256 is 89+255.
	p50, _, _, count := snapshot.Quantiles(SeriesReceivedToImport)
	if count != FrameTimelineSampleLimit || p50 != 344 {
		t.Fatalf("p50 = %v count = %d, want 344 over 512 retained samples", p50, count)
	}
}

func TestFrameTimelineRejectsNegativeDurations(t *testing.T) {
	timeline := NewFrameTimeline()
	timeline.ObserveFrameImported(-time.Millisecond, 0, 0, 0)
	timeline.ObserveFrameImported(0, -time.Millisecond, 0, 0)
	timeline.ObserveFrameImported(0, 0, -time.Millisecond, 0)
	timeline.ObserveFrameImported(0, 0, 0, -time.Millisecond)
	snapshot := timeline.Snapshot()
	if snapshot.Swapped != 0 || snapshot.Imported != 0 {
		t.Fatalf("negative elapsed durations were recorded: %+v", snapshot)
	}
	if total := snapshot.Series[seriesIndex(SeriesReceivedToImport)].Total; total != 0 {
		t.Fatalf("negative duration was recorded as %d sample(s)", total)
	}
}

func TestFrameTimelineDrainResetsTheWindow(t *testing.T) {
	timeline := NewFrameTimeline()
	timeline.ObserveFrameImported(4*time.Millisecond, 2*time.Millisecond, time.Millisecond, 5*time.Millisecond)
	drained := timeline.Drain()
	if drained.Swapped != 1 {
		t.Fatalf("drained swapped = %d, want 1", drained.Swapped)
	}
	if drained.Series[seriesIndex(SeriesReceivedToImport)].Total != 1 {
		t.Fatalf("drained series total = %d, want 1", drained.Series[seriesIndex(SeriesReceivedToImport)].Total)
	}
	if drained.Series[seriesIndex(SeriesReceivedToImport)].Values[0] != 4 {
		t.Fatalf("drained sample = %v, want 4", drained.Series[seriesIndex(SeriesReceivedToImport)].Values[0])
	}
	second := timeline.Drain()
	if second.Swapped != 0 || second.Series[seriesIndex(SeriesReceivedToImport)].Total != 0 {
		t.Fatalf("drain did not reset the window: %+v", second)
	}
}

func TestFrameTimelineConcurrentCollectionIsExact(t *testing.T) {
	timeline := NewFrameTimeline()
	const goroutines = 8
	const perGoroutine = 500
	var wait sync.WaitGroup
	for range goroutines {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range perGoroutine {
				timeline.ObserveFrameReceived()
				timeline.ObserveFrameImported(time.Millisecond, 0, 0, time.Millisecond)
			}
		}()
	}
	wait.Wait()

	snapshot := timeline.Snapshot()
	if snapshot.Received != goroutines*perGoroutine {
		t.Fatalf("received = %d, want %d", snapshot.Received, goroutines*perGoroutine)
	}
	if snapshot.Swapped != goroutines*perGoroutine {
		t.Fatalf("swapped = %d, want %d", snapshot.Swapped, goroutines*perGoroutine)
	}
	series := snapshot.Series[seriesIndex(SeriesReceivedToImport)]
	if series.Total != goroutines*perGoroutine {
		t.Fatalf("series total = %d, want %d", series.Total, goroutines*perGoroutine)
	}
	if series.Overwritten != uint64(goroutines*perGoroutine-FrameTimelineSampleLimit) {
		t.Fatalf("overwritten = %d, want %d", series.Overwritten, goroutines*perGoroutine-FrameTimelineSampleLimit)
	}
}

func TestRecorderPipelineIsAbsentUntilEnabled(t *testing.T) {
	recorder := NewRecorder()
	recorder.Start(time.Unix(0, 0))
	recorder.ObserveGDKFrameReceived()
	recorder.ObserveGDKPaintCycle()

	if recorder.FrameTimelineEnabled() {
		t.Fatal("timeline reported enabled before EnableFrameTimeline")
	}
	snapshot, ok := recorder.MaybeSnapshot(time.Unix(2, 0), time.Second)
	if !ok {
		t.Fatal("expected a snapshot after the interval elapsed")
	}
	if snapshot.GDKPipeline != nil {
		t.Fatal("gdk_pipeline present while the timeline was disabled")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "gdk_pipeline") {
		t.Fatalf("disabled snapshot JSON contains gdk_pipeline: %s", encoded)
	}
}

func TestRecorderPipelineSnapshotShapeAndReset(t *testing.T) {
	recorder := NewRecorder()
	recorder.Start(time.Unix(0, 0))
	recorder.EnableFrameTimeline()
	received := time.Unix(100, 0)
	recorder.ObserveGDKImport(received, received.Add(time.Millisecond), received.Add(3*time.Millisecond), received.Add(7*time.Millisecond))
	recorder.ObserveGDKFrameReceived()
	recorder.ObserveGDKPendingReplaced()
	recorder.ObserveGDKPaintCycle()
	recorder.ObserveGDKSwapsOverwrittenBeforePaint(1)
	recorder.ObserveGDKFeedback(true)

	snapshot, ok := recorder.MaybeSnapshot(time.Unix(2, 0), time.Second)
	if !ok || snapshot.GDKPipeline == nil {
		t.Fatal("expected an enabled gdk_pipeline snapshot")
	}
	encoded, err := json.Marshal(snapshot.GDKPipeline)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		SchemaVersion    int    `json:"schema_version"`
		FramesReceived   uint64 `json:"frames_received"`
		PendingReplaced  uint64 `json:"pending_replaced"`
		FramesImported   uint64 `json:"frames_imported"`
		FramesSwapped    uint64 `json:"frames_swapped"`
		GTKPaintCycles   uint64 `json:"gtk_paint_cycles"`
		Overwritten      uint64 `json:"swaps_overwritten_before_paint"`
		FeedbackAvail    uint64 `json:"feedback_available"`
		FeedbackUnavail  uint64 `json:"feedback_unavailable"`
		ReceivedToImport struct {
			Samples     uint64  `json:"samples"`
			Retained    int     `json:"retained"`
			Available   bool    `json:"available"`
			Sampled     bool    `json:"sampled"`
			SampleLimit int     `json:"sample_limit"`
			P50         float64 `json:"p50"`
			Note        string  `json:"quantile_note"`
		} `json:"received_to_import_ms"`
		QueueWait json.RawMessage `json:"queue_wait_ms"`
		Elapsed   json.RawMessage `json:"import_elapsed_ms"`
		ToSwap    json.RawMessage `json:"received_to_swap_ms"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.SchemaVersion != FrameTimelineSchemaVersion {
		t.Fatalf("schema version = %d, want %d", decoded.SchemaVersion, FrameTimelineSchemaVersion)
	}
	if decoded.FramesReceived != 1 || decoded.PendingReplaced != 1 || decoded.FramesImported != 1 ||
		decoded.FramesSwapped != 1 || decoded.GTKPaintCycles != 1 || decoded.Overwritten != 1 ||
		decoded.FeedbackAvail != 1 || decoded.FeedbackUnavail != 0 {
		t.Fatalf("unexpected counters: %+v", decoded)
	}
	if decoded.ReceivedToImport.Samples != 1 || decoded.ReceivedToImport.Retained != 1 ||
		!decoded.ReceivedToImport.Sampled || decoded.ReceivedToImport.SampleLimit != FrameTimelineSampleLimit {
		t.Fatalf("unexpected series report: %+v", decoded.ReceivedToImport)
	}
	if !decoded.ReceivedToImport.Available {
		t.Fatal("a series with a retained sample reported available=false")
	}
	if decoded.ReceivedToImport.P50 != 3 {
		t.Fatalf("received_to_import p50 = %v, want 3", decoded.ReceivedToImport.P50)
	}
	if decoded.ReceivedToImport.Note == "" {
		t.Fatal("series report does not label the quantile method")
	}
	for name, raw := range map[string]json.RawMessage{
		"queue_wait_ms":       decoded.QueueWait,
		"import_elapsed_ms":   decoded.Elapsed,
		"received_to_swap_ms": decoded.ToSwap,
	} {
		if len(raw) == 0 {
			t.Fatalf("series %s missing from the report", name)
		}
	}

	// A second window starts from zero rather than repeating the first.
	second, ok := recorder.MaybeSnapshot(time.Unix(4, 0), time.Second)
	if !ok || second.GDKPipeline == nil {
		t.Fatal("expected a second enabled snapshot")
	}
	var secondDecoded struct {
		FramesSwapped   uint64 `json:"frames_swapped"`
		FramesReceived  uint64 `json:"frames_received"`
		FeedbackAvail   uint64 `json:"feedback_available"`
		FeedbackUnavail uint64 `json:"feedback_unavailable"`
	}
	secondJSON, err := json.Marshal(second.GDKPipeline)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if err := json.Unmarshal(secondJSON, &secondDecoded); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if secondDecoded.FramesSwapped != 0 || secondDecoded.FramesReceived != 0 ||
		secondDecoded.FeedbackAvail != 0 || secondDecoded.FeedbackUnavail != 0 {
		t.Fatalf("second window did not start from zero: %s", secondJSON)
	}

	// An empty window reports its series as unavailable rather than as a
	// measured zero latency.
	var emptySeries struct {
		QueueWait struct {
			Available bool `json:"available"`
			Retained  int  `json:"retained"`
		} `json:"queue_wait_ms"`
	}
	if err := json.Unmarshal(secondJSON, &emptySeries); err != nil {
		t.Fatalf("unmarshal second series: %v", err)
	}
	if emptySeries.QueueWait.Available || emptySeries.QueueWait.Retained != 0 {
		t.Fatalf("empty series reported available: %+v", emptySeries.QueueWait)
	}
}

func TestRecorderImportRejectsInconsistentClockOrder(t *testing.T) {
	recorder := NewRecorder()
	recorder.EnableFrameTimeline()
	base := time.Unix(50, 0)
	// Enqueued after the import started: a negative queue wait must not be sampled.
	recorder.ObserveGDKImport(base, base.Add(5*time.Millisecond), base.Add(2*time.Millisecond), base.Add(6*time.Millisecond))
	// Zero timestamps describe a frame that never carried profiling metadata.
	recorder.ObserveGDKImport(time.Time{}, base, base, base)

	snapshot := recorder.timeline.Load().Snapshot()
	if snapshot.Swapped != 0 {
		t.Fatalf("swapped = %d, want 0 for rejected samples", snapshot.Swapped)
	}
	if total := snapshot.Series[seriesIndex(SeriesQueueWait)].Total; total != 0 {
		t.Fatalf("queue wait recorded %d sample(s) with a negative elapsed time", total)
	}
}

func TestRecorderObserveMethodsAreNilSafe(t *testing.T) {
	var recorder *Recorder
	recorder.EnableFrameTimeline()
	recorder.DisableFrameTimeline()
	recorder.ObserveGDKFrameReceived()
	recorder.ObserveGDKPendingReplaced()
	recorder.ObserveGDKImport(time.Unix(0, 0), time.Unix(0, 0), time.Unix(0, 0), time.Unix(0, 0))
	recorder.ObserveGDKImportWithoutSwap()
	recorder.ObserveGDKPaintCycle()
	recorder.ObserveGDKSwapsOverwrittenBeforePaint(1)
	recorder.ObserveGDKFeedback(true)
	if recorder.FrameTimelineEnabled() {
		t.Fatal("nil recorder reported an enabled timeline")
	}
}

func TestRecorderDisableFrameTimelineStopsCollection(t *testing.T) {
	recorder := NewRecorder()
	recorder.EnableFrameTimeline()
	recorder.ObserveGDKFrameReceived()
	recorder.DisableFrameTimeline()
	recorder.ObserveGDKFrameReceived()

	if timeline := recorder.timeline.Load(); timeline != nil {
		t.Fatal("timeline retained after DisableFrameTimeline")
	}
}
