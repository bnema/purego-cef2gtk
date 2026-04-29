package cef2gtk

import (
	"errors"
	"testing"
)

func TestDiagnosticsRecorderSnapshot(t *testing.T) {
	r := newDiagnosticsRecorder()
	r.RecordAcceleratedPaint()
	r.RecordUnsupportedPaint()
	r.RecordImportFailure(errors.New("boom"))
	r.RecordRenderFailure(errors.New("draw"))

	d := r.Snapshot()
	if d.AcceleratedPaints != 1 || d.UnsupportedPaints != 1 || d.ImportFailures != 1 || d.RenderFailures != 1 || d.AcceleratedPaintErrors != 2 {
		t.Fatalf("unexpected diagnostics: %+v", d)
	}
	if len(d.Events) != 4 {
		t.Fatalf("events=%d, want 4", len(d.Events))
	}
	wantKinds := []string{"accelerated-paint", "unsupported-paint", "import-failure", "render-failure"}
	wantMessages := []string{"", "", "boom", "draw"}
	for i := range wantKinds {
		if d.Events[i].Kind != wantKinds[i] || d.Events[i].Message != wantMessages[i] {
			t.Fatalf("event[%d]=%+v, want kind %q message %q", i, d.Events[i], wantKinds[i], wantMessages[i])
		}
	}
	d.Events[0].Kind = "mutated"
	if got := r.Snapshot().Events[0].Kind; got == "mutated" {
		t.Fatalf("snapshot events alias recorder storage")
	}
}
