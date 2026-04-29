package cef2gtk

import (
	"errors"
	"testing"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
)

// fakeRenderQueue keeps handler tests small and records queue/error behavior
// directly; generated mocks would add noise for this package-local interface.
type fakeRenderQueue struct {
	err          error
	importCalled bool
	renderCalled bool
	queued       bool
}

func (f *fakeRenderQueue) ImportCopyAndQueue(*cef.AcceleratedPaintInfo) (gtkgl.QueuedFrame, error) {
	f.importCalled = true
	if f.err != nil {
		return gtkgl.QueuedFrame{}, f.err
	}
	return gtkgl.QueuedFrame{}, nil
}
func (f *fakeRenderQueue) QueueRender()                 { f.queued = true }
func (f *fakeRenderQueue) InitializeOnGTKThread() error { return nil }
func (f *fakeRenderQueue) RenderQueuedOnGTKThread() error {
	f.renderCalled = true
	return f.err
}
func (f *fakeRenderQueue) Close() {}

func TestOnPaintFailLoudRecordsAndHooks(t *testing.T) {
	d := newDiagnosticsRecorder()
	called := false
	h := &renderHandler{diag: d, staticHooks: Hooks{OnUnsupportedPaint: func() { called = true }}}
	h.OnPaint(nil, cef.PaintElementTypePetView, nil, []byte{1, 2}, 10, 10)
	if !called {
		t.Fatalf("unsupported paint hook not called")
	}
	if got := d.Snapshot().UnsupportedPaints; got != 1 {
		t.Fatalf("unsupported paints=%d, want 1", got)
	}
}

func TestOnAcceleratedPaintErrorHook(t *testing.T) {
	want := errors.New("copy failed")
	f := &fakeRenderQueue{err: want}
	d := newDiagnosticsRecorder()
	var got error
	h := &renderHandler{renderer: f, diag: d, staticHooks: Hooks{OnError: func(err error) { got = err }}}
	h.OnAcceleratedPaint(nil, cef.PaintElementTypePetView, nil, nil)
	if !f.importCalled {
		t.Fatalf("accelerated renderer not called")
	}
	if !errors.Is(got, want) {
		t.Fatalf("OnError got %v, want %v", got, want)
	}
	diag := d.Snapshot()
	if diag.AcceleratedPaints != 1 || diag.AcceleratedPaintErrors != 1 || diag.ImportFailures != 1 || diag.RenderFailures != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diag)
	}
	if f.queued {
		t.Fatalf("queued render after failed import/copy")
	}
}

func TestOnAcceleratedPaintQueuesRenderOnSuccess(t *testing.T) {
	f := &fakeRenderQueue{}
	h := &renderHandler{renderer: f, diag: newDiagnosticsRecorder()}
	h.OnAcceleratedPaint(nil, cef.PaintElementTypePetView, nil, nil)
	if !f.importCalled || !f.queued {
		t.Fatalf("importCalled=%v queued=%v, want both true", f.importCalled, f.queued)
	}
}

func TestRenderSignalErrorHookAndDiagnostics(t *testing.T) {
	want := errors.New("render failed")
	f := &fakeRenderQueue{err: want}
	d := newDiagnosticsRecorder()
	var got error
	v := &View{renderer: f, diag: d}
	_ = v.RenderHandler(Hooks{OnError: func(err error) { got = err }})
	if ok := v.renderOnGTKThread(); ok {
		t.Fatalf("renderOnGTKThread returned true after render error")
	}
	if !f.renderCalled {
		t.Fatalf("renderer not called")
	}
	if !errors.Is(got, want) {
		t.Fatalf("OnError got %v, want %v", got, want)
	}
	diag := d.Snapshot()
	if diag.RenderFailures != 1 {
		t.Fatalf("RenderFailures=%d, want 1", diag.RenderFailures)
	}
	if len(diag.Events) != 1 || diag.Events[0].Kind != "render-failure" || diag.Events[0].Message != want.Error() {
		t.Fatalf("unexpected diagnostics events: %+v", diag.Events)
	}
}
