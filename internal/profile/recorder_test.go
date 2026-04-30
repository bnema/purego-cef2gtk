package profile

import (
	"encoding/json"
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
