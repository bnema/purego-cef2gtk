package cef2gtk

import (
	"os"
	"testing"
	"time"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
	internalprofile "github.com/bnema/purego-cef2gtk/internal/profile"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gobject"
	"github.com/bnema/puregotk/v4/gtk"
)

type visibilityRecordingHost struct {
	cef.BrowserHost
	hiddenStates []int32
}

func (h *visibilityRecordingHost) WasHidden(hidden int32) {
	h.hiddenStates = append(h.hiddenStates, hidden)
}

func TestViewMapShowAndUnmapHideVisibilitySignalsAreIdempotent(t *testing.T) {
	host := &visibilityRecordingHost{}
	v := &View{input: gtkgl.NewInputBridge(host, 1)}

	v.handleVisibilitySignal(true)  // map
	v.handleVisibilitySignal(true)  // show
	v.handleVisibilitySignal(false) // unmap
	v.handleVisibilitySignal(false) // hide

	if len(host.hiddenStates) != 2 || host.hiddenStates[0] != 0 || host.hiddenStates[1] != 1 {
		t.Fatalf("visibility notifications = %v, want [0 1]", host.hiddenStates)
	}
}

func TestNewViewWithOptionsRejectsInvalidBackendBeforeGTK(t *testing.T) {
	t.Setenv(backendEnvVar, "invalid")
	if got := NewViewWithOptions(ViewOptions{Backend: BackendGLArea}); got != nil {
		t.Fatal("NewViewWithOptions returned view for invalid env backend")
	}
}

func TestViewSizeScaleAndObservers(t *testing.T) {
	v := &View{}
	if w, h := v.Size(); w != 1 || h != 1 {
		t.Fatalf("initial size=(%d,%d), want (1,1)", w, h)
	}
	if got := v.DeviceScaleFactor(); got != 1 {
		t.Fatalf("initial scale=%v, want 1", got)
	}
	v.storeObservedScale(1.2)
	if got := v.DeviceScaleFactor(); got != 1.2 {
		t.Fatalf("stored fractional scale=%v, want 1.2", got)
	}

	called := false
	var gotW, gotH int32
	remove := v.AddSizeObserver(func(w, h int32) {
		called = true
		gotW, gotH = w, h
	})
	if called {
		t.Fatal("observer called for synthetic fallback size")
	}

	v.cachedWidth.Store(100)
	v.cachedHeight.Store(50)
	v.emitSizeHooks(100, 50)
	if !called || gotW != 100 || gotH != 50 {
		t.Fatalf("observer size=(%d,%d) called=%v, want (100,50) true", gotW, gotH, called)
	}
	remove()
	v.emitSizeHooks(200, 75)
	if gotW != 100 || gotH != 50 {
		t.Fatalf("observer called after remove: (%d,%d)", gotW, gotH)
	}
}

func TestFirstPresentationWaitsForSubsequentAfterPaintAndRunsOnce(t *testing.T) {
	var afterPaint func()
	connects := 0
	disconnects := 0
	events := []string{}
	v := &View{
		hooks: Hooks{
			OnFirstDMABUFTextureSwap: func() { events = append(events, "swap") },
			OnFirstPresentation:      func() { events = append(events, "present") },
		},
		frameClockAfterPaintConnect: func(fn func()) func() {
			connects++
			afterPaint = fn
			return func() { disconnects++ }
		},
	}

	v.recordFirstDMABUFTextureSwap()
	v.recordFirstDMABUFTextureSwap()
	if got, want := events, []string{"swap"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("events before after-paint = %v, want %v", got, want)
	}
	if connects != 1 || afterPaint == nil {
		t.Fatalf("after-paint connects=%d callback=%v, want one callback", connects, afterPaint != nil)
	}

	afterPaint()
	afterPaint()
	if got, want := events, []string{"swap", "present"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("events after after-paint = %v, want %v", got, want)
	}
	if disconnects != 1 {
		t.Fatalf("after-paint disconnects=%d, want 1", disconnects)
	}
}

func TestFirstPresentationWaitsForTextureSwapDespiteObservationSignals(t *testing.T) {
	var afterPaint func()
	connects := 0
	presented := 0
	v := &View{
		hooks: Hooks{OnFirstPresentation: func() { presented++ }},
		frameClockAfterPaintConnect: func(fn func()) func() {
			connects++
			afterPaint = fn
			return func() {}
		},
	}

	// Map, show, realize, and size paths all reach handleObservationSignal.
	for range 4 {
		v.handleObservationSignal()
	}
	if connects != 0 {
		t.Fatalf("observation signals connected after-paint %d times before texture swap, want 0", connects)
	}
	if afterPaint != nil {
		afterPaint()
	}
	if presented != 0 {
		t.Fatalf("presentation callbacks before texture swap = %d, want 0", presented)
	}

	v.recordFirstDMABUFTextureSwap()
	if connects != 1 || afterPaint == nil {
		t.Fatalf("texture swap after-paint connects=%d callback=%v, want one callback", connects, afterPaint != nil)
	}
	afterPaint()
	if presented != 1 {
		t.Fatalf("presentation callbacks after texture swap = %d, want 1", presented)
	}
}

func TestFirstPresentationRetriesAfterPostSwapFrameClockBecomesAvailable(t *testing.T) {
	var afterPaint func()
	connects := 0
	presented := 0
	v := &View{
		hooks: Hooks{OnFirstPresentation: func() { presented++ }},
		frameClockAfterPaintConnect: func(fn func()) func() {
			connects++
			if connects == 1 {
				return nil
			}
			afterPaint = fn
			return func() {}
		},
	}

	v.recordFirstDMABUFTextureSwap()
	if connects != 1 || afterPaint != nil {
		t.Fatalf("initial post-swap frame-clock connect=(%d, %v), want (1, false)", connects, afterPaint != nil)
	}

	v.handleObservationSignal()
	if connects != 2 || afterPaint == nil {
		t.Fatalf("post-swap observation retry connects=%d callback=%v, want one retry callback", connects, afterPaint != nil)
	}
	afterPaint()
	if presented != 1 {
		t.Fatalf("presentation callbacks after delayed frame clock = %d, want 1", presented)
	}
}

func TestDestroyDisconnectsPendingFirstPresentationAfterPaint(t *testing.T) {
	var afterPaint func()
	disconnects := 0
	presented := 0
	v := &View{
		hooks: Hooks{OnFirstPresentation: func() { presented++ }},
		frameClockAfterPaintConnect: func(fn func()) func() {
			afterPaint = fn
			return func() { disconnects++ }
		},
	}
	v.recordFirstDMABUFTextureSwap()
	if afterPaint == nil {
		t.Fatal("first texture swap did not arm after-paint")
	}

	if err := v.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	afterPaint()
	if disconnects != 1 {
		t.Fatalf("after-paint disconnects=%d, want 1", disconnects)
	}
	if presented != 0 {
		t.Fatalf("presentation callback ran after teardown: %d", presented)
	}
}

func TestDeviceScaleFactorAppliesViewScaleMultiplier(t *testing.T) {
	v := &View{}
	v.setScaleMultiplier(1.2)
	v.storeObservedScale(1.2)

	if got := v.DeviceScaleFactor(); got != float32(1.44) {
		t.Fatalf("effective device scale=%v, want 1.44", got)
	}
	if got := v.observedScale(); got != 1.2 {
		t.Fatalf("raw observed scale=%v, want 1.2", got)
	}
}

func TestAddSizeObserverImmediatelyCallsWithObservedRealSizeIncludingOneByOne(t *testing.T) {
	v := &View{}
	v.cachedWidth.Store(1)
	v.cachedHeight.Store(1)

	called := false
	remove := v.AddSizeObserver(func(w, h int32) {
		called = true
		if w != 1 || h != 1 {
			t.Fatalf("observer size=(%d,%d), want (1,1)", w, h)
		}
	})
	defer remove()

	if !called {
		t.Fatal("observer not called for real observed 1x1 size")
	}
}

func TestDisconnectSurfaceSignalsKeepsCallbacksAlive(t *testing.T) {
	v := &View{
		surfaceLayoutFunc:   func(gdk.Surface, int, int) {},
		surfaceWidthNotify:  func(gobject.Object, *gobject.ParamSpec) {},
		surfaceHeightNotify: func(gobject.Object, *gobject.ParamSpec) {},
		surfaceScaleNotify:  func(gobject.Object, *gobject.ParamSpec) {},
	}

	v.disconnectSurfaceSignals()

	if v.surfaceLayoutFunc == nil || v.surfaceWidthNotify == nil || v.surfaceHeightNotify == nil || v.surfaceScaleNotify == nil {
		t.Fatal("surface signal callbacks must remain alive after disconnect")
	}
}

func TestEffectiveInputWidgetPrefersAttachedInputWidget(t *testing.T) {
	renderWidget := &gtk.Widget{}
	inputWidget := &gtk.Widget{}
	v := &View{widget: renderWidget, input: &gtkgl.InputBridge{}, inputWidget: inputWidget}

	if got := v.effectiveInputWidget(); got != inputWidget {
		t.Fatalf("effectiveInputWidget = %p, want input widget %p", got, inputWidget)
	}
}

func TestEffectiveInputWidgetFallsBackToRenderWidgetWhenInputNotAttached(t *testing.T) {
	renderWidget := &gtk.Widget{}
	inputWidget := &gtk.Widget{}
	v := &View{widget: renderWidget, inputWidget: inputWidget}

	if got := v.effectiveInputWidget(); got != renderWidget {
		t.Fatalf("effectiveInputWidget = %p, want render widget %p", got, renderWidget)
	}
}

func TestDestroyClearsInputWidget(t *testing.T) {
	v := &View{widget: &gtk.Widget{}, inputWidget: &gtk.Widget{}}

	if err := v.Destroy(); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if v.inputWidget != nil {
		t.Fatalf("Destroy() left inputWidget set")
	}
}

func TestResolveObservedDimension(t *testing.T) {
	tests := []struct {
		name      string
		cached    int32
		allocated int32
		widget    int32
		want      int32
	}{
		{
			name:      "uses allocated size when present",
			cached:    640,
			allocated: 800,
			widget:    1,
			want:      800,
		},
		{
			name:      "preserves cached size across synthetic one pixel fallback",
			cached:    640,
			allocated: 0,
			widget:    1,
			want:      640,
		},
		{
			name:      "real allocated one pixel replaces larger cached size",
			cached:    640,
			allocated: 1,
			widget:    1,
			want:      1,
		},
		{
			name:      "allows initial one pixel bootstrap before any real size",
			cached:    0,
			allocated: 0,
			widget:    1,
			want:      1,
		},
		{
			name:      "accepts widget width above sentinel when allocation missing",
			cached:    640,
			allocated: 0,
			widget:    777,
			want:      777,
		},
		{
			name:      "keeps zero when nothing observed yet",
			cached:    0,
			allocated: 0,
			widget:    0,
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveObservedDimension(tt.cached, tt.allocated, tt.widget); got != tt.want {
				t.Fatalf("resolveObservedDimension(%d, %d, %d) = %d, want %d", tt.cached, tt.allocated, tt.widget, got, tt.want)
			}
		})
	}
}

func TestNewViewWidgetGLAreaBasics(t *testing.T) {
	if os.Getenv("PUREGO_CEF2GTK_LIVE_GTK_TEST") == "" {
		t.Skip("requires live GTK runtime; set PUREGO_CEF2GTK_LIVE_GTK_TEST=1")
	}
	t.Setenv(backendEnvVar, "glarea")
	v := NewViewWithOptions(ViewOptions{Backend: BackendGLArea})
	if v == nil {
		t.Fatal("NewView returned nil")
	}
	t.Cleanup(func() {
		if err := v.Destroy(); err != nil {
			t.Errorf("Destroy: %v", err)
		}
	})
	if got := v.Backend(); got != BackendGLArea {
		t.Fatalf("Backend = %q, want %q", got, BackendGLArea)
	}
	if v.GLArea() == nil {
		t.Fatalf("GLArea nil")
	}
	if v.Widget() == nil {
		t.Fatalf("Widget nil")
	}
	if d := v.Diagnostics(); d.AcceleratedPaints != 0 || d.UnsupportedPaints != 0 ||
		d.AcceleratedPaintErrors != 0 || d.ImportFailures != 0 || d.RenderFailures != 0 {
		t.Fatalf("unexpected initial diagnostics: %+v", d)
	}
}

// ---------------------------------------------------------------------------
// GTK frame-clock observer
// ---------------------------------------------------------------------------

// frameObserverTestRenderer satisfies the renderer interface with no GTK calls
// so paint-cycle lifecycle can be driven directly.
type frameObserverTestRenderer struct {
	profiler *internalprofile.Recorder
	swaps    uint64
}

func (r *frameObserverTestRenderer) InitializeOnGTKThread() error { return nil }

func (r *frameObserverTestRenderer) ImportAndQueueOnGTKThread(*cef.AcceleratedPaintInfo) (gtkgl.QueuedFrame, error) {
	return gtkgl.QueuedFrame{}, nil
}

func (r *frameObserverTestRenderer) QueueRender() {}

func (r *frameObserverTestRenderer) RenderQueuedOnGTKThread() error { return nil }

func (r *frameObserverTestRenderer) InvalidateOnGTKThread() {}

func (r *frameObserverTestRenderer) SetProfiler(profiler *internalprofile.Recorder) {
	r.profiler = profiler
}

func (r *frameObserverTestRenderer) Close() {}

func (r *frameObserverTestRenderer) SwapSequence() uint64 { return r.swaps }

// fakeFrameClockSource is a scripted frame clock: feedback for a counter can be
// marked complete-and-available, complete-and-unavailable, or still pending.
type fakeFrameClockSource struct {
	identity  uintptr
	counter   int64
	feedback  map[int64]feedbackState
	queries   []int64
	absentFor map[int64]bool
}

type feedbackState struct {
	available bool
	resolved  bool
}

func (f *fakeFrameClockSource) Identity() uintptr { return f.identity }

func (f *fakeFrameClockSource) FrameCounter() int64 { return f.counter }

func (f *fakeFrameClockSource) Feedback(counter int64) (bool, bool) {
	f.queries = append(f.queries, counter)
	if f.absentFor[counter] {
		return false, true
	}
	state, ok := f.feedback[counter]
	if !ok {
		return false, false
	}
	return state.available, state.resolved
}

type frameObserverHarness struct {
	view        *View
	renderer    *frameObserverTestRenderer
	recorder    *internalprofile.Recorder
	connects    int
	disconnects int
	source      *fakeFrameClockSource
}

func newFrameObserverHarness(t *testing.T, source *fakeFrameClockSource) *frameObserverHarness {
	t.Helper()
	harness := &frameObserverHarness{
		renderer: &frameObserverTestRenderer{},
		source:   source,
	}
	harness.view = &View{renderer: harness.renderer}
	if err := harness.view.ConfigureProfiling(ProfileOptions{Enabled: true, Interval: time.Hour}); err != nil {
		t.Fatalf("ConfigureProfiling: %v", err)
	}
	harness.recorder = harness.renderer.profiler
	if harness.recorder == nil {
		t.Fatal("profiling did not install a recorder")
	}
	harness.view.frameObserverClock = func() (frameClockSource, bool) {
		if harness.source == nil {
			return nil, false
		}
		return harness.source, true
	}
	harness.view.frameObserverConnect = func(_ *gdk.FrameClock, _ *func(gdk.FrameClock)) uint {
		harness.connects++
		return uint(harness.connects)
	}
	harness.view.frameObserverDisconnect = func(*gdk.FrameClock, uint) {
		harness.disconnects++
	}
	harness.view.frameObserverSwapSeq = func() uint64 { return harness.renderer.swaps }
	return harness
}

func (h *frameObserverHarness) cycle(counter int64) {
	h.source.counter = counter
	h.view.onFramePaintCycle(h.view.frameObserver.generation)
}

func (h *frameObserverHarness) window(t *testing.T) internalprofile.FrameTimelineSnapshot {
	t.Helper()
	report, ok := h.recorder.PipelineSnapshot()
	if !ok {
		t.Fatal("frame timeline is not enabled")
	}
	return report.FrameTimelineSnapshot()
}

func TestFrameObserverAttachesOnceAndKeepsOneObserverPerClock(t *testing.T) {
	harness := newFrameObserverHarness(t, &fakeFrameClockSource{identity: 1})
	for range 3 {
		harness.view.syncFrameObserver()
	}
	if harness.connects != 1 {
		t.Fatalf("connects = %d, want exactly 1 for repeated map/show/realize", harness.connects)
	}
	if !harness.view.frameObserver.attached {
		t.Fatal("observer not attached after sync")
	}

	// Hide and show again: one disconnect, one new attach on the same clock.
	harness.view.detachFrameObserver()
	harness.view.syncFrameObserver()
	if harness.disconnects != 1 || harness.connects != 2 {
		t.Fatalf("disconnects = %d connects = %d, want 1 and 2", harness.disconnects, harness.connects)
	}
}

func TestFrameObserverNewClockRetiresFeedbackAndNeverCrossAttributes(t *testing.T) {
	first := &fakeFrameClockSource{identity: 11}
	harness := newFrameObserverHarness(t, first)
	harness.view.syncFrameObserver()
	harness.cycle(1)

	second := &fakeFrameClockSource{identity: 22}
	harness.source = second
	harness.view.syncFrameObserver()

	if harness.connects != 2 || harness.disconnects != 1 {
		t.Fatalf("connects = %d disconnects = %d, want 2 and 1", harness.connects, harness.disconnects)
	}
	window := harness.window(t)
	if window.FeedbackUnavailable != 1 || window.FeedbackAvailable != 0 {
		t.Fatalf("previous clock's association was not retired as unavailable: %+v", window)
	}
	if len(first.queries) != 0 {
		t.Fatalf("previous clock was queried after the swap: %v", first.queries)
	}
	if window.PaintCycles != 1 {
		t.Fatalf("paint cycles = %d, want 1 attributed to the first clock only", window.PaintCycles)
	}

	// The new clock's own cycle is counted, and its association is only queried on
	// the following cycle, on the new identity.
	harness.cycle(5)
	if window := harness.window(t); window.PaintCycles != 2 {
		t.Fatalf("paint cycles = %d, want 2", window.PaintCycles)
	}
	if len(second.queries) != 0 {
		t.Fatalf("second clock was queried before a following cycle: %v", second.queries)
	}
	harness.cycle(6)
	if len(second.queries) != 1 || second.queries[0] != 5 {
		t.Fatalf("second clock queries = %v, want [5]", second.queries)
	}
	if window := harness.window(t); window.PaintCycles != 3 {
		t.Fatalf("paint cycles = %d, want 3", window.PaintCycles)
	}
}

func TestFrameObserverResolvesFeedbackOnLaterCycles(t *testing.T) {
	source := &fakeFrameClockSource{
		identity: 7,
		feedback: map[int64]feedbackState{
			1: {available: true, resolved: true},
			2: {available: false, resolved: true},
		},
		absentFor: map[int64]bool{4: true},
	}
	harness := newFrameObserverHarness(t, source)
	harness.view.syncFrameObserver()

	harness.cycle(1) // queues association for counter 1
	harness.cycle(2) // resolves 1 as available, queues 2
	harness.cycle(3) // resolves 2 as unavailable, queues 3
	harness.cycle(4) // 3 is still pending, queues 4
	harness.cycle(5) // resolves absent timings for 4 as unavailable

	window := harness.window(t)
	if window.FeedbackAvailable != 1 {
		t.Fatalf("feedback available = %d, want 1", window.FeedbackAvailable)
	}
	// Counter 2 reported incomplete feedback and counter 4 had no recorded
	// timings: both are unavailable, never zero latency.
	if window.FeedbackUnavailable != 2 {
		t.Fatalf("feedback unavailable = %d, want 2", window.FeedbackUnavailable)
	}
	if window.PaintCycles != 5 {
		t.Fatalf("paint cycles = %d, want 5", window.PaintCycles)
	}
	if pending := len(harness.view.frameObserver.pending); pending != 2 {
		t.Fatalf("pending associations = %d, want 2 (counters 3 and 5)", pending)
	}

	// Advancing past the association limit expires the rest as unavailable.
	harness.cycle(gtkFrameAssociationLimit + 6)
	window = harness.window(t)
	if window.FeedbackUnavailable != 4 {
		t.Fatalf("feedback unavailable after expiry = %d, want 4", window.FeedbackUnavailable)
	}
	if pending := len(harness.view.frameObserver.pending); pending != 1 {
		t.Fatalf("pending associations after expiry = %d, want 1", pending)
	}
}

func TestFrameObserverCountsSwapsOverwrittenBeforePaint(t *testing.T) {
	harness := newFrameObserverHarness(t, &fakeFrameClockSource{identity: 3})
	harness.view.syncFrameObserver()

	harness.renderer.swaps = 1
	harness.cycle(1) // first swap lands in this cycle
	harness.renderer.swaps = 4
	harness.cycle(2) // three swaps, one presented: two overwritten opportunities

	window := harness.window(t)
	if window.OverwrittenBeforePaint != 2 {
		t.Fatalf("overwritten before paint = %d, want 2", window.OverwrittenBeforePaint)
	}
	if window.PaintCycles != 2 {
		t.Fatalf("paint cycles = %d, want 2", window.PaintCycles)
	}
}

func TestFrameObserverDoesNotCountSwapsFromBeforeAttach(t *testing.T) {
	harness := newFrameObserverHarness(t, &fakeFrameClockSource{identity: 31})
	// Swaps completed long before this observer existed must not be reported as
	// overwritten opportunities: the swap sequence is a lifetime counter.
	harness.renderer.swaps = 5000
	harness.view.syncFrameObserver()
	harness.cycle(1)

	if window := harness.window(t); window.OverwrittenBeforePaint != 0 {
		t.Fatalf("first cycle after attach reported %d overwritten swaps, want 0", window.OverwrittenBeforePaint)
	}

	// Hide and show again: reattaching must rebaseline against the same rule.
	harness.view.detachFrameObserver()
	harness.renderer.swaps = 5100
	harness.view.syncFrameObserver()
	harness.cycle(2)
	if window := harness.window(t); window.OverwrittenBeforePaint != 0 {
		t.Fatalf("first cycle after reattach reported %d overwritten swaps, want 0", window.OverwrittenBeforePaint)
	}

	// A swap that happens while attached is still counted.
	harness.renderer.swaps = 5103
	harness.cycle(3)
	if window := harness.window(t); window.OverwrittenBeforePaint != 2 {
		t.Fatalf("overwritten before paint = %d, want 2", window.OverwrittenBeforePaint)
	}
}

func TestFrameObserverPendingAssociationsAreBounded(t *testing.T) {
	source := &fakeFrameClockSource{identity: 5, feedback: map[int64]feedbackState{}}
	harness := newFrameObserverHarness(t, source)
	harness.view.syncFrameObserver()

	const cycles = gtkFrameAssociationLimit + 10
	for counter := int64(1); counter <= cycles; counter++ {
		// Every cycle stays unresolved: the association buffer must still be bounded.
		source.feedback[counter] = feedbackState{resolved: false}
		harness.cycle(counter)
	}
	if got := len(harness.view.frameObserver.pending); got > gtkFrameAssociationLimit {
		t.Fatalf("pending associations = %d, want at most %d", got, gtkFrameAssociationLimit)
	}
	// The overflow was reported as unavailable rather than dropped silently.
	window := harness.window(t)
	if window.FeedbackUnavailable == 0 {
		t.Fatal("expired associations were not reported as unavailable")
	}
	if window.PaintCycles != cycles {
		t.Fatalf("paint cycles = %d, want %d", window.PaintCycles, cycles)
	}
}

func TestFrameObserverDetachRetiresPendingAsUnavailable(t *testing.T) {
	source := &fakeFrameClockSource{identity: 9}
	harness := newFrameObserverHarness(t, source)
	harness.view.syncFrameObserver()
	harness.cycle(1)
	harness.cycle(2)

	harness.view.detachFrameObserver()

	window := harness.window(t)
	if window.FeedbackUnavailable != 2 || window.FeedbackAvailable != 0 {
		t.Fatalf("detach did not retire both associations: %+v", window)
	}
	if len(harness.view.frameObserver.pending) != 0 {
		t.Fatalf("pending associations survived detach: %d", len(harness.view.frameObserver.pending))
	}
}

func TestFrameObserverStaleCallbackIsIgnoredAfterReattach(t *testing.T) {
	harness := newFrameObserverHarness(t, &fakeFrameClockSource{identity: 4})
	harness.view.syncFrameObserver()
	staleGeneration := harness.view.frameObserver.generation
	staleCallback := harness.view.frameObserver.callback

	harness.view.detachFrameObserver()
	harness.view.syncFrameObserver()

	// A callback queued by the previous attachment must not be attributed to the
	// new clock generation.
	harness.view.onFramePaintCycle(staleGeneration)
	if window := harness.window(t); window.PaintCycles != 0 {
		t.Fatalf("stale callback produced %d paint cycle(s)", window.PaintCycles)
	}
	if staleCallback == nil {
		t.Fatal("observer callback was not installed")
	}
}

func TestFrameObserverProfilingDisabledDetachesWithoutReattaching(t *testing.T) {
	harness := newFrameObserverHarness(t, &fakeFrameClockSource{identity: 2})
	harness.view.syncFrameObserver()
	connects := harness.connects

	if err := harness.view.ConfigureProfiling(ProfileOptions{Enabled: false}); err != nil {
		t.Fatalf("disable profiling: %v", err)
	}
	harness.view.syncFrameObserver()
	if harness.view.frameObserver.attached {
		t.Fatal("observer still attached after profiling was disabled")
	}
	if harness.connects != connects {
		t.Fatalf("connects = %d, want no reconnect after disabling profiling", harness.connects)
	}
	harness.view.syncFrameObserver()
	if harness.connects != connects {
		t.Fatalf("connects = %d, want no attach without a profiler", harness.connects)
	}
}

func TestFrameObserverDestroyedViewDoesNotReattach(t *testing.T) {
	harness := newFrameObserverHarness(t, &fakeFrameClockSource{identity: 6})
	harness.view.syncFrameObserver()
	connects := harness.connects
	harness.view.destroyed.Store(true)

	harness.view.syncFrameObserver()

	if harness.view.frameObserver.attached {
		t.Fatal("destroyed view attached an observer")
	}
	if harness.connects != connects {
		t.Fatalf("connects = %d, want no attach after destroy", harness.connects)
	}
}

func TestFrameObserverMissingClockIsHandledGracefully(t *testing.T) {
	harness := newFrameObserverHarness(t, nil)
	harness.view.syncFrameObserver()
	if harness.view.frameObserver.attached {
		t.Fatal("observer attached without a frame clock")
	}
	if harness.connects != 0 {
		t.Fatalf("connects = %d, want 0 without a frame clock", harness.connects)
	}
}
