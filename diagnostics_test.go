package cef2gtk

import (
	"errors"
	"fmt"
	"testing"
)

func TestViewDiagnosticsIncludesBackend(t *testing.T) {
	v := &View{backend: BackendGDKDMABUF, diag: newDiagnosticsRecorder()}
	d := v.Diagnostics()
	if d.Backend != "gdk-dmabuf" {
		t.Fatalf("Backend = %q, want gdk-dmabuf", d.Backend)
	}
}

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

func TestDiagnosticsRecorderRingBufferWraparound(t *testing.T) {
	r := newDiagnosticsRecorder()
	for i := 0; i < maxDiagnosticEvents+3; i++ {
		r.RecordImportFailure(fmt.Errorf("err-%03d", i))
	}
	d := r.Snapshot()
	if len(d.Events) != maxDiagnosticEvents {
		t.Fatalf("events=%d, want %d", len(d.Events), maxDiagnosticEvents)
	}
	if d.Events[0].Message != "err-003" {
		t.Fatalf("first event message=%q, want err-003", d.Events[0].Message)
	}
	if d.Events[len(d.Events)-1].Message != fmt.Sprintf("err-%03d", maxDiagnosticEvents+2) {
		t.Fatalf("last event message=%q", d.Events[len(d.Events)-1].Message)
	}
	d.Events[0].Kind = "mutated"
	if got := r.Snapshot().Events[0].Kind; got == "mutated" {
		t.Fatalf("snapshot events alias recorder storage")
	}
}
