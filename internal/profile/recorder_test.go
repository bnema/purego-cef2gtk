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
