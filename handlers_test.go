package cef2gtk

import (
	"errors"
	"testing"
	"time"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
	internalprofile "github.com/bnema/purego-cef2gtk/internal/profile"
)

// fakeRenderQueue keeps handler tests small and records queue/error behavior
// directly; generated mocks would add noise for this package-local interface.
type fakeRenderQueue struct {
	err            error
	importCalled   bool
	renderCalled   bool
	queued         bool
	closeStarted   chan struct{}
	continueClose  chan struct{}
	closeStartedOK bool
	events         *[]string
}

type fakeAsyncRenderQueue struct {
	fakeRenderQueue
	asyncCalled bool
}

func (f *fakeRenderQueue) ImportAndQueueOnGTKThread(*cef.AcceleratedPaintInfo) (gtkgl.QueuedFrame, error) {
	f.importCalled = true
	if f.events != nil {
		*f.events = append(*f.events, "import")
	}
	if f.err != nil {
		return gtkgl.QueuedFrame{}, f.err
	}
	return gtkgl.QueuedFrame{}, nil
}

func (f *fakeAsyncRenderQueue) ImportAndQueueAsync(*cef.AcceleratedPaintInfo, func(error)) error {
	f.asyncCalled = true
	return f.err
}
func (f *fakeRenderQueue) QueueRender()                 { f.queued = true }
func (f *fakeRenderQueue) InitializeOnGTKThread() error { return nil }
func (f *fakeRenderQueue) RenderQueuedOnGTKThread() error {
	f.renderCalled = true
	return f.err
}
func (f *fakeRenderQueue) InvalidateOnGTKThread()                { f.Close() }
func (f *fakeRenderQueue) SetProfiler(*internalprofile.Recorder) {}
func (f *fakeRenderQueue) Close() {
	if f.closeStarted != nil && !f.closeStartedOK {
		f.closeStartedOK = true
		close(f.closeStarted)
	}
	if f.continueClose != nil {
		<-f.continueClose
	}
}

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

func TestFirstAcceleratedPaintHookCanDestroyView(t *testing.T) {
	f := &fakeRenderQueue{}
	v := &View{renderer: f, diag: newDiagnosticsRecorder()}
	h := v.RenderHandler(Hooks{OnFirstAcceleratedPaint: func() {
		if err := v.Destroy(); err != nil {
			t.Errorf("Destroy: %v", err)
		}
	}})

	done := make(chan struct{})
	go func() {
		h.OnAcceleratedPaint(nil, cef.PaintElementTypePetView, nil, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("accelerated paint deadlocked when its first-paint hook destroyed the view")
	}
	if f.importCalled || f.queued {
		t.Fatal("accelerated paint imported or queued after its hook destroyed the view")
	}
}

func TestFirstAcceleratedPaintHookPrecedesImportAndRunsOnce(t *testing.T) {
	events := []string{}
	f := &fakeRenderQueue{events: &events}
	v := &View{renderer: f, diag: newDiagnosticsRecorder()}
	h := v.RenderHandler(Hooks{OnFirstAcceleratedPaint: func() { events = append(events, "accelerated") }})

	h.OnAcceleratedPaint(nil, cef.PaintElementTypePetView, nil, nil)
	h.OnAcceleratedPaint(nil, cef.PaintElementTypePetView, nil, nil)

	if got, want := events, []string{"accelerated", "import", "import"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if !f.importCalled || !f.queued {
		t.Fatal("accelerated paint was not imported and queued")
	}
}

func TestGLAreaReportsUnsupportedDMABUFCompletionWithoutFalseSwap(t *testing.T) {
	f := &fakeRenderQueue{}
	v := &View{backend: BackendGLArea, renderer: f, diag: newDiagnosticsRecorder()}
	events := []string{}
	h := v.RenderHandler(Hooks{
		OnFirstAcceleratedPaint:  func() { events = append(events, "accelerated") },
		OnDMABUFUnsupported:      func() { events = append(events, "unsupported") },
		OnFirstDMABUFTextureSwap: func() { events = append(events, "swap") },
		OnFirstPresentation:      func() { events = append(events, "present") },
	})

	h.OnAcceleratedPaint(nil, cef.PaintElementTypePetView, nil, nil)

	if got, want := len(events), 2; got != want || events[0] != "accelerated" || events[1] != "unsupported" {
		t.Fatalf("events = %v, want [accelerated unsupported]", events)
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

func TestOnAcceleratedPaintUsesAsyncRendererWhenAvailable(t *testing.T) {
	f := &fakeAsyncRenderQueue{}
	h := &renderHandler{renderer: f, diag: newDiagnosticsRecorder()}
	h.OnAcceleratedPaint(nil, cef.PaintElementTypePetView, nil, nil)
	if !f.asyncCalled {
		t.Fatal("async renderer was not used")
	}
	if f.importCalled {
		t.Fatal("sync import path should not run for async renderer")
	}
	if !f.queued {
		t.Fatal("render was not queued after async import scheduling")
	}
}

func TestOnAcceleratedPaintSuppressesTransientGLAreaLifecycleHook(t *testing.T) {
	f := &fakeRenderQueue{err: gtkgl.ErrGLAreaNotRealized}
	d := newDiagnosticsRecorder()
	called := false
	h := &renderHandler{renderer: f, diag: d, staticHooks: Hooks{OnError: func(error) { called = true }}}

	h.OnAcceleratedPaint(nil, cef.PaintElementTypePetView, nil, nil)

	if !f.importCalled {
		t.Fatal("accelerated renderer not called")
	}
	if called {
		t.Fatal("OnError called for transient GtkGLArea lifecycle error")
	}
	diag := d.Snapshot()
	if diag.AcceleratedPaints != 1 || diag.ImportFailures != 1 {
		t.Fatalf("unexpected diagnostics: %+v", diag)
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

func TestGetViewRectFallsBackToOneByOneWithoutObservedSize(t *testing.T) {
	h := &renderHandler{view: &View{}}
	rect := cef.Rect{}

	h.GetViewRect(nil, &rect)

	if rect.Width != 1 || rect.Height != 1 {
		t.Fatalf("rect=(%d,%d), want (1,1)", rect.Width, rect.Height)
	}
}

func TestScreenGeometryUsesViewLocalRoot(t *testing.T) {
	setOSRBackingScaleEnv(t, "")
	v := &View{}
	v.cachedWidth.Store(640)
	v.cachedHeight.Store(480)
	h := &renderHandler{view: v}

	var root cef.Rect
	if ok := h.GetRootScreenRect(nil, &root); ok != 1 {
		t.Fatalf("GetRootScreenRect returned %d, want 1", ok)
	}
	if root.X != 0 || root.Y != 0 || root.Width != 640 || root.Height != 480 {
		t.Fatalf("root rect=%+v, want 640x480 at origin", root)
	}

	var screenX, screenY int32
	if ok := h.GetScreenPoint(nil, 123, 456, &screenX, &screenY); ok != 1 {
		t.Fatalf("GetScreenPoint returned %d, want 1", ok)
	}
	if screenX != 123 || screenY != 456 {
		t.Fatalf("screen point=%d,%d, want 123,456", screenX, screenY)
	}
}

func TestScreenPointScalesCoordinatesWithoutSizeClamping(t *testing.T) {
	setOSRBackingScaleEnv(t, "")
	v := &View{}
	v.storeObservedScale(1.25)
	h := &renderHandler{view: v}

	var screenX, screenY int32
	if ok := h.GetScreenPoint(nil, 0, -2, &screenX, &screenY); ok != 1 {
		t.Fatalf("GetScreenPoint returned %d, want 1", ok)
	}
	if screenX != 0 || screenY != -3 {
		t.Fatalf("screen point=%d,%d, want 0,-3", screenX, screenY)
	}

	if ok := h.GetScreenPoint(nil, 123, 456, &screenX, &screenY); ok != 1 {
		t.Fatalf("GetScreenPoint returned %d, want 1", ok)
	}
	if screenX != 153 || screenY != 570 {
		t.Fatalf("screen point=%d,%d, want 153,570", screenX, screenY)
	}
}

func TestGetScreenInfoUsesViewSizeAndScale(t *testing.T) {
	setOSRBackingScaleEnv(t, "")
	v := &View{}
	v.cachedWidth.Store(640)
	v.cachedHeight.Store(480)
	v.storeObservedScale(1.25)
	h := &renderHandler{view: v}
	info := cef.NewScreenInfo()
	if ok := h.GetScreenInfo(nil, &info); ok != 1 {
		t.Fatalf("GetScreenInfo returned %d, want 1", ok)
	}
	if info.DeviceScaleFactor != 1.25 {
		t.Fatalf("scale=%v, want 1.25", info.DeviceScaleFactor)
	}
	if info.Rect.Width != 640 || info.Rect.Height != 480 || info.AvailableRect.Width != 640 || info.AvailableRect.Height != 480 {
		t.Fatalf("unexpected rects: rect=%+v available=%+v", info.Rect, info.AvailableRect)
	}
}

func TestGetScreenInfoAndViewRectUseForcedBackingScale(t *testing.T) {
	setOSRBackingScaleEnv(t, "1")
	v := &View{}
	v.cachedWidth.Store(640)
	v.cachedHeight.Store(480)
	v.storeObservedScale(1.25)
	h := &renderHandler{view: v}
	var rect cef.Rect
	h.GetViewRect(nil, &rect)
	if rect.Width != 800 || rect.Height != 600 {
		t.Fatalf("view rect=%+v, want 800x600", rect)
	}
	info := cef.NewScreenInfo()
	if ok := h.GetScreenInfo(nil, &info); ok != 1 {
		t.Fatalf("GetScreenInfo returned %d, want 1", ok)
	}
	if info.DeviceScaleFactor != 1 {
		t.Fatalf("scale=%v, want 1 when backing scale enabled", info.DeviceScaleFactor)
	}
	if info.Rect.Width != 800 || info.Rect.Height != 600 {
		t.Fatalf("unexpected rects: rect=%+v", info.Rect)
	}
}

func TestGetScreenInfoAndViewRectUseAutoBackingScaleAboveOne(t *testing.T) {
	setOSRBackingScaleEnv(t, "auto")
	v := &View{}
	v.cachedWidth.Store(640)
	v.cachedHeight.Store(480)
	v.storeObservedScale(1.25)
	h := &renderHandler{view: v}

	var rect cef.Rect
	h.GetViewRect(nil, &rect)
	if rect.Width != 800 || rect.Height != 600 {
		t.Fatalf("view rect=%+v, want 800x600", rect)
	}

	info := cef.NewScreenInfo()
	if ok := h.GetScreenInfo(nil, &info); ok != 1 {
		t.Fatalf("GetScreenInfo returned %d, want 1", ok)
	}
	if info.DeviceScaleFactor != 1 {
		t.Fatalf("scale=%v, want 1 when auto backing scale enabled", info.DeviceScaleFactor)
	}
	if info.Rect.Width != 800 || info.Rect.Height != 600 {
		t.Fatalf("unexpected rects: rect=%+v", info.Rect)
	}
}

func TestStaleHandlerAfterDestroyDoesNotCallRenderer(t *testing.T) {
	f := &fakeRenderQueue{}
	v := &View{renderer: f, diag: newDiagnosticsRecorder()}
	h := v.RenderHandler(Hooks{})

	if err := v.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Late CEF callback on the previously-obtained handler.
	h.OnAcceleratedPaint(nil, cef.PaintElementTypePetView, nil, nil)

	if f.importCalled {
		t.Fatal("stale handler called accelerated renderer after Destroy")
	}
	if f.queued {
		t.Fatal("stale handler queued render after Destroy")
	}

	d := v.Diagnostics()
	if d.StaleAcceleratedPaints != 1 {
		t.Fatalf("StaleAcceleratedPaints=%d, want 1", d.StaleAcceleratedPaints)
	}
}

func TestStaleHandlerDuringDestroyDoesNotRaceRendererTeardown(t *testing.T) {
	f := &fakeRenderQueue{closeStarted: make(chan struct{}), continueClose: make(chan struct{})}
	v := &View{renderer: f, diag: newDiagnosticsRecorder()}
	h := v.RenderHandler(Hooks{})

	destroyDone := make(chan error, 1)
	go func() { destroyDone <- v.Destroy() }()
	<-f.closeStarted

	paintDone := make(chan struct{})
	go func() {
		h.OnAcceleratedPaint(nil, cef.PaintElementTypePetView, nil, nil)
		close(paintDone)
	}()

	select {
	case <-paintDone:
		t.Fatal("accelerated paint completed while renderer teardown was in progress")
	default:
	}

	close(f.continueClose)
	if err := <-destroyDone; err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	<-paintDone

	if f.importCalled {
		t.Fatal("stale handler called accelerated renderer during Destroy")
	}
	if f.queued {
		t.Fatal("stale handler queued render during Destroy")
	}
}

func TestOnTextSelectionChangedHook(t *testing.T) {
	var got string
	h := &renderHandler{staticHooks: Hooks{OnTextSelectionChanged: func(selectedText string, _ *cef.Range) {
		got = selectedText
	}}}
	h.OnTextSelectionChanged(nil, "hello", nil)
	if got != "hello" {
		t.Fatalf("selected text hook got %q, want hello", got)
	}
}
