package profile

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestRecorderSnapshotAggregatesAndResetsWindow(t *testing.T) {
	r := NewRecorder()
	start := time.Unix(100, 0)
	r.Start(start)
	r.RecordFrameReceived()
	r.RecordFrameQueued()
	r.RecordFrameRendered()
	r.RecordGTKWait(2 * time.Millisecond)
	r.RecordImportCPU(3 * time.Millisecond)
	r.RecordCopyGPU(4 * time.Millisecond)

	snap, ok := r.MaybeSnapshot(start.Add(time.Second), time.Second)
	if !ok {
		t.Fatal("snapshot not emitted")
	}
	if snap.FramesReceived != 1 || snap.FramesQueued != 1 || snap.FramesRendered != 1 {
		t.Fatalf("unexpected frame counts: %+v", snap)
	}
	if snap.GTKWaitCPU.Count != 1 || snap.GTKWaitCPU.Total != 2*time.Millisecond {
		t.Fatalf("GTKWaitCPU = %+v", snap.GTKWaitCPU)
	}
	if snap.CopyGPU.Count != 1 || snap.CopyGPU.Total != 4*time.Millisecond {
		t.Fatalf("CopyGPU = %+v", snap.CopyGPU)
	}

	snap, ok = r.MaybeSnapshot(start.Add(2*time.Second), time.Second)
	if !ok {
		t.Fatal("second snapshot not emitted")
	}
	if snap.FramesReceived != 0 || snap.GTKWaitCPU.Count != 0 {
		t.Fatalf("window did not reset: %+v", snap)
	}
}

func TestRecorderSnapshotIncludesInputAndBeginFrameCounters(t *testing.T) {
	r := NewRecorder()
	start := time.Unix(100, 0)
	r.Start(start)
	r.RecordScroll(1.5, -2.25)
	r.RecordScroll(-0.5, 0.25)
	r.RecordExternalBeginFrameSent()
	r.RecordExternalBeginFrameSent()

	snap, ok := r.MaybeSnapshot(start.Add(time.Second), time.Second)
	if !ok {
		t.Fatal("snapshot not emitted")
	}
	if snap.ScrollEvents != 2 {
		t.Fatalf("ScrollEvents = %d, want 2", snap.ScrollEvents)
	}
	if snap.ScrollDXSum != 1.0 || snap.ScrollDYSum != -2.0 {
		t.Fatalf("scroll sums = (%v,%v), want (1,-2)", snap.ScrollDXSum, snap.ScrollDYSum)
	}
	if snap.ScrollAbsDXSum != 2.0 || snap.ScrollAbsDYSum != 2.5 {
		t.Fatalf("scroll abs sums = (%v,%v), want (2,2.5)", snap.ScrollAbsDXSum, snap.ScrollAbsDYSum)
	}
	if snap.ExternalBeginFramesSent != 2 {
		t.Fatalf("ExternalBeginFramesSent = %d, want 2", snap.ExternalBeginFramesSent)
	}

	snap, ok = r.MaybeSnapshot(start.Add(2*time.Second), time.Second)
	if !ok {
		t.Fatal("second snapshot not emitted")
	}
	if snap.ScrollEvents != 0 || snap.ExternalBeginFramesSent != 0 {
		t.Fatalf("hot-path counters did not reset: %+v", snap)
	}
}

func TestRecorderSnapshotDrainsSuppressedLeavesDuringDrag(t *testing.T) {
	r := NewRecorder()
	start := time.Unix(100, 0)
	r.Start(start)
	r.RecordSuppressedLeaveDuringDrag()
	r.RecordSuppressedLeaveDuringDrag()

	snap, ok := r.MaybeSnapshot(start.Add(time.Second), time.Second)
	if !ok {
		t.Fatal("snapshot not emitted")
	}
	if snap.SuppressedLeavesDuringDrag != 2 {
		t.Fatalf("SuppressedLeavesDuringDrag = %d, want 2", snap.SuppressedLeavesDuringDrag)
	}

	snap, ok = r.MaybeSnapshot(start.Add(2*time.Second), time.Second)
	if !ok {
		t.Fatal("second snapshot not emitted")
	}
	if snap.SuppressedLeavesDuringDrag != 0 {
		t.Fatalf("SuppressedLeavesDuringDrag = %d after drain, want 0", snap.SuppressedLeavesDuringDrag)
	}
}

func TestRecorderSnapshotDrainsPressesWithoutMatchedRelease(t *testing.T) {
	r := NewRecorder()
	start := time.Unix(100, 0)
	r.Start(start)
	r.RecordPressWithoutMatchedRelease()
	r.RecordPressWithoutMatchedRelease()

	snap, ok := r.MaybeSnapshot(start.Add(time.Second), time.Second)
	if !ok {
		t.Fatal("snapshot not emitted")
	}
	if snap.PressesWithoutMatchedRelease != 2 {
		t.Fatalf("PressesWithoutMatchedRelease = %d, want 2", snap.PressesWithoutMatchedRelease)
	}

	snap, ok = r.MaybeSnapshot(start.Add(2*time.Second), time.Second)
	if !ok {
		t.Fatal("second snapshot not emitted")
	}
	if snap.PressesWithoutMatchedRelease != 0 {
		t.Fatalf("PressesWithoutMatchedRelease = %d after drain, want 0", snap.PressesWithoutMatchedRelease)
	}
}

func TestRecorderDiagnosticCountersConcurrentConsistency(t *testing.T) {
	r := NewRecorder()
	start := time.Unix(100, 0)
	r.Start(start)

	const workers = 8
	const eventsPerWorker = 1000
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for range eventsPerWorker {
				r.RecordSuppressedLeaveDuringDrag()
				r.RecordPressWithoutMatchedRelease()
			}
		}()
	}
	wg.Wait()

	snap, ok := r.MaybeSnapshot(start.Add(time.Second), time.Second)
	if !ok {
		t.Fatal("snapshot not emitted")
	}
	want := uint64(workers * eventsPerWorker)
	if snap.SuppressedLeavesDuringDrag != want {
		t.Fatalf("SuppressedLeavesDuringDrag = %d, want %d", snap.SuppressedLeavesDuringDrag, want)
	}
	if snap.PressesWithoutMatchedRelease != want {
		t.Fatalf("PressesWithoutMatchedRelease = %d, want %d", snap.PressesWithoutMatchedRelease, want)
	}
}

func TestRecorderScrollSnapshotConcurrentConsistency(t *testing.T) {
	r := NewRecorder()
	start := time.Unix(100, 0)
	r.Start(start)

	const workers = 8
	const eventsPerWorker = 1000
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for range eventsPerWorker {
				r.RecordScroll(1, -1)
			}
		}()
	}
	wg.Wait()

	snap, ok := r.MaybeSnapshot(start.Add(time.Second), time.Second)
	if !ok {
		t.Fatal("snapshot not emitted")
	}
	want := uint64(workers * eventsPerWorker)
	if snap.ScrollEvents != want {
		t.Fatalf("ScrollEvents = %d, want %d", snap.ScrollEvents, want)
	}
	if snap.ScrollDXSum != float64(want) || snap.ScrollDYSum != -float64(want) {
		t.Fatalf("scroll sums = (%v,%v), want (%v,%v)", snap.ScrollDXSum, snap.ScrollDYSum, float64(want), -float64(want))
	}
	if snap.ScrollAbsDXSum != float64(want) || snap.ScrollAbsDYSum != float64(want) {
		t.Fatalf("scroll abs sums = (%v,%v), want (%v,%v)", snap.ScrollAbsDXSum, snap.ScrollAbsDYSum, float64(want), float64(want))
	}
}

func TestSnapshotJSONIncludesDiagnosticCounterNames(t *testing.T) {
	b, err := json.Marshal(Snapshot{
		SuppressedLeavesDuringDrag:   2,
		PressesWithoutMatchedRelease: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["suppressed_leaves_during_drag"] != float64(2) {
		t.Fatalf("suppressed_leaves_during_drag = %v, want 2", got["suppressed_leaves_during_drag"])
	}
	if got["presses_without_matched_release"] != float64(3) {
		t.Fatalf("presses_without_matched_release = %v, want 3", got["presses_without_matched_release"])
	}
}

func TestSnapshotJSONUsesMillisecondFields(t *testing.T) {
	stats := DurationStats{}
	stats.Add(1500 * time.Microsecond)
	b, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]float64
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["avg_ms"] != 1.5 || got["total_ms"] != 1.5 {
		t.Fatalf("json stats = %s", b)
	}
}
