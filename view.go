package cef2gtk

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgdk"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
	internalprofile "github.com/bnema/purego-cef2gtk/internal/profile"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gobject"
	"github.com/bnema/puregotk/v4/gtk"
)

var ErrNilView = errors.New("nil View")

const (
	scaleTraceEnvVar = "PUREGO_CEF2GTK_TRACE_SCALE"
	osrTraceEnvVar   = "PUREGO_CEF2GTK_TRACE_OSR"
)

// Hooks contains optional callbacks invoked by the public CEF render adapter.
type Hooks struct {
	OnUnsupportedPaint     func()
	OnError                func(error)
	OnTextSelectionChanged func(selectedText string, selectedRange *cef.Range)
}

type renderer interface {
	InitializeOnGTKThread() error
	ImportAndQueueOnGTKThread(*cef.AcceleratedPaintInfo) (gtkgl.QueuedFrame, error)
	QueueRender()
	RenderQueuedOnGTKThread() error
	InvalidateOnGTKThread()
	SetProfiler(*internalprofile.Recorder)
	Close()
}

// View is a GTK-backed CEF OSR view.
type View struct {
	backend            Backend
	widget             *gtk.Widget
	area               *gtk.GLArea
	renderer           renderer
	signalObject       *gobject.Object
	input              *gtkgl.InputBridge
	diag               *diagnosticsRecorder
	hooks              Hooks
	handler            *renderHandler
	renderFunc         func(gtk.GLArea, uintptr) bool
	unrealizeFunc      func(gtk.Widget)
	sizeTickFunc       *gtk.TickCallback
	renderHandlerID    uint
	unrealizeHandlerID uint
	sizeTickID         uint
	widthNotify        func(gobject.Object, *gobject.ParamSpec)
	heightNotify       func(gobject.Object, *gobject.ParamSpec)
	widthHandlerID     uint
	heightHandlerID    uint
	cachedWidth        atomic.Int32
	cachedHeight       atomic.Int32
	scaleBits          atomic.Uint64
	inputScaleOverride atomic.Uint64
	scaleTraceCount    atomic.Uint64
	osrTraceCount      atomic.Uint64
	sizeHooksMu        sync.Mutex
	sizeHooks          map[uint64]func(width, height int32)
	nextSizeHookID     uint64
	profileMu          sync.Mutex
	profileEnabled     atomic.Bool
	profile            *internalprofile.Recorder
	profileOptions     ProfileOptions
}

// NewView creates an accelerated CEF view using BackendAuto or the
// PUREGO_CEF2GTK_BACKEND override when it is set.
func NewView() *View {
	return NewViewWithOptions(ViewOptions{Backend: BackendAuto})
}

// NewViewWithOptions creates an accelerated CEF view with explicit options.
// When PUREGO_CEF2GTK_BACKEND is set, it intentionally overrides opts.Backend
// for diagnostics and deployment-level backend selection.
func NewViewWithOptions(opts ViewOptions) *View {
	opts, err := opts.normalized()
	if err != nil {
		return nil
	}
	if envBackend, ok, err := backendFromEnv(); err != nil {
		return nil
	} else if ok {
		opts.Backend = envBackend
	}

	backend := opts.Backend
	if backend == BackendAuto {
		if v := newGDKDMABUFView(opts.Profile); v != nil {
			return v
		}
		backend = BackendGLArea
	}
	switch backend {
	case BackendGLArea:
		return newGLAreaView(opts.Profile)
	case BackendGDKDMABUF:
		return newGDKDMABUFView(opts.Profile)
	default:
		return nil
	}
}

func newGLAreaView(profile ProfileOptions) *View {
	area := gtk.NewGLArea()
	if area == nil {
		return nil
	}
	v := &View{backend: BackendGLArea, widget: &area.Widget, area: area, diag: newDiagnosticsRecorder()}
	area.SetAutoRender(false)
	v.renderer = gtkgl.NewAcceleratedRenderer(area)
	v.connectRenderSignal()
	if profile.Enabled {
		if err := v.ConfigureProfiling(profile); err != nil {
			return nil
		}
	}
	return v
}

func newGDKDMABUFView(profile ProfileOptions) *View {
	renderer, err := gtkgdk.NewRenderer(false)
	if err != nil || renderer == nil || renderer.Widget() == nil {
		return nil
	}
	v := &View{backend: BackendGDKDMABUF, widget: renderer.Widget(), renderer: renderer, diag: newDiagnosticsRecorder()}
	v.connectRenderSignal()
	if profile.Enabled {
		if err := v.ConfigureProfiling(profile); err != nil {
			return nil
		}
	}
	return v
}

func (v *View) observedSize() (width, height int32, ok bool) {
	if v == nil {
		return 0, 0, false
	}
	width = v.cachedWidth.Load()
	height = v.cachedHeight.Load()
	return width, height, width > 0 && height > 0
}

func (v *View) connectRenderSignal() {
	if v == nil || v.widget == nil || v.renderer == nil {
		return
	}
	v.updateCachedSizeOnGTKThread()
	v.signalObject = &v.widget.Object
	v.widthNotify = func(gobject.Object, *gobject.ParamSpec) { v.updateCachedSizeOnGTKThread() }
	v.heightNotify = func(gobject.Object, *gobject.ParamSpec) { v.updateCachedSizeOnGTKThread() }
	v.widthHandlerID = v.signalObject.ConnectNotifyWithDetail("width", &v.widthNotify)
	v.heightHandlerID = v.signalObject.ConnectNotifyWithDetail("height", &v.heightNotify)
	v.unrealizeFunc = func(gtk.Widget) {
		if v.renderer != nil {
			v.renderer.InvalidateOnGTKThread()
		}
	}
	v.unrealizeHandlerID = v.widget.ConnectUnrealize(&v.unrealizeFunc)
	v.connectSizeTick()
	if v.area == nil {
		return
	}
	v.renderFunc = func(_ gtk.GLArea, _ uintptr) bool {
		v.updateCachedSizeOnGTKThread()
		return v.renderOnGTKThread()
	}
	v.renderHandlerID = v.area.ConnectRender(&v.renderFunc)
}

func (v *View) connectSizeTick() {
	if v == nil || v.widget == nil || v.sizeTickID != 0 {
		return
	}
	cb := new(gtk.TickCallback)
	*cb = func(_, _, _ uintptr) bool {
		if v == nil || v.widget == nil {
			return false
		}
		v.updateCachedSizeOnGTKThread()
		return true
	}
	v.sizeTickFunc = cb
	v.sizeTickID = v.widget.AddTickCallback(cb, 0, nil)
}

func (v *View) updateCachedSizeOnGTKThread() {
	if v == nil || v.widget == nil {
		return
	}
	width := int32(v.widget.GetAllocatedWidth())
	height := int32(v.widget.GetAllocatedHeight())
	if width <= 0 {
		width = int32(v.widget.GetWidth())
	}
	if height <= 0 {
		height = int32(v.widget.GetHeight())
	}
	changed := false
	if width > 0 {
		changed = v.cachedWidth.Swap(width) != width || changed
	}
	if height > 0 {
		changed = v.cachedHeight.Swap(height) != height || changed
	}
	prevScale := v.observedScale()
	obs := observeWidgetScale(v.widget)
	scaleChanged := prevScale != obs.scale
	v.storeObservedScale(obs.scale)
	if scaleChanged && v.input != nil {
		v.input.SetScale(v.inputScaleForObservedScale(obs.scale))
	}
	if changed || scaleChanged {
		v.traceScaleObservation("widget-update", width, height, prevScale, obs)
	}
	if (changed || scaleChanged) && width > 0 && height > 0 {
		v.emitSizeHooks(width, height)
	}
}

func (v *View) cachedSize() (int32, int32) {
	width, height := v.Size()
	return width, height
}

// Size returns the last positive widget size observed on the GTK thread. Before
// the widget has a real size, CEF requires a non-zero fallback, so Size returns
// 1x1.
func (v *View) Size() (int32, int32) {
	if v == nil {
		return 1, 1
	}
	width := v.cachedWidth.Load()
	height := v.cachedHeight.Load()
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		height = 1
	}
	return width, height
}

// DeviceScaleFactor returns the last GTK surface scale observed for the view.
// Values <= 0 are normalized to 1.
func (v *View) DeviceScaleFactor() float32 {
	if v == nil {
		return 1
	}
	return float32(v.observedScale())
}

func (v *View) observedScale() float64 {
	if v == nil {
		return 1
	}
	bits := v.scaleBits.Load()
	if bits == 0 {
		return 1
	}
	return normalizeDeviceScale(math.Float64frombits(bits))
}

func (v *View) storeObservedScale(scale float64) {
	if v == nil {
		return
	}
	v.scaleBits.Store(math.Float64bits(normalizeDeviceScale(scale)))
}

func normalizeDeviceScale(scale float64) float64 {
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 {
		return 1
	}
	return scale
}

type widgetScaleObservation struct {
	scale              float64
	widgetScaleFactor  int
	surfaceScale       float64
	surfaceScaleFactor int
	hasSurface         bool
}

func observeWidgetScale(widget *gtk.Widget) widgetScaleObservation {
	obs := widgetScaleObservation{scale: 1}
	if widget == nil {
		return obs
	}
	obs.widgetScaleFactor = widget.GetScaleFactor()
	if surface := widgetSurface(widget); surface != nil {
		obs.hasSurface = true
		if scale := surface.GetScale(); scale > 0 && !math.IsNaN(scale) && !math.IsInf(scale, 0) {
			obs.surfaceScale = scale
		}
		if scaleFactor := surface.GetScaleFactor(); scaleFactor > 0 {
			obs.surfaceScaleFactor = scaleFactor
		}
	}
	switch {
	case obs.surfaceScale > 0:
		obs.scale = obs.surfaceScale
	case obs.surfaceScaleFactor > 0:
		obs.scale = float64(obs.surfaceScaleFactor)
	case obs.widgetScaleFactor > 0:
		obs.scale = float64(obs.widgetScaleFactor)
	}
	obs.scale = normalizeDeviceScale(obs.scale)
	obs.surfaceScale = normalizeDeviceScale(obs.surfaceScale)
	return obs
}

func widgetSurface(widget *gtk.Widget) *gdk.Surface {
	if widget == nil {
		return nil
	}
	native := widget.GetNative()
	if native == nil {
		return nil
	}
	return native.GetSurface()
}

func (v *View) traceScaleObservation(reason string, width, height int32, prevScale float64, obs widgetScaleObservation) {
	if os.Getenv(scaleTraceEnvVar) == "" || v == nil || v.scaleTraceCount.Add(1) > 32 {
		return
	}
	fmt.Fprintf(os.Stderr,
		"cef2gtk-scale reason=%s backend=%s widget_logical=%dx%d scale=%.3f prev_scale=%.3f widget_scale_factor=%d surface_scale=%.3f surface_scale_factor=%d has_surface=%t input_attached=%t\n",
		reason, v.backend.String(), width, height, obs.scale, prevScale, obs.widgetScaleFactor, obs.surfaceScale, obs.surfaceScaleFactor, obs.hasSurface, v.input != nil)
}

func traceOSREnabled() bool {
	return os.Getenv(scaleTraceEnvVar) != "" || os.Getenv(osrTraceEnvVar) != ""
}

func (v *View) traceViewRect(width, height int32) {
	if !traceOSREnabled() || v == nil || v.osrTraceCount.Add(1) > 96 {
		return
	}
	widgetWidth, widgetHeight := v.cachedSize()
	scale := float64(v.DeviceScaleFactor())
	expectedWidth, expectedHeight := expectedDeviceSize(widgetWidth, widgetHeight, scale)
	fmt.Fprintf(os.Stderr,
		"cef2gtk-osr-geometry callback=view-rect backend=%s widget_logical=%dx%d osr_rect=%dx%d device_scale=%.3f backing_scale_enabled=%t expected_device=%dx%d\n",
		v.backend.String(), widgetWidth, widgetHeight, width, height, scale, v.osrBackingScaleEnabled(), expectedWidth, expectedHeight)
}

func (v *View) traceScreenInfo(width, height int32, scale float32) {
	if !traceOSREnabled() || v == nil || v.osrTraceCount.Add(1) > 96 {
		return
	}
	widgetWidth, widgetHeight := v.cachedSize()
	surfaceScale := float64(v.DeviceScaleFactor())
	expectedWidth, expectedHeight := expectedDeviceSize(widgetWidth, widgetHeight, surfaceScale)
	reportedExpectedWidth, reportedExpectedHeight := expectedDeviceSize(width, height, float64(scale))
	fmt.Fprintf(os.Stderr,
		"cef2gtk-screen-info backend=%s widget_logical=%dx%d screen_rect=%dx%d device_scale=%.3f surface_scale=%.3f backing_scale_enabled=%t widget_expected_device=%dx%d reported_expected_device=%dx%d\n",
		v.backend.String(), widgetWidth, widgetHeight, width, height, scale, surfaceScale, v.osrBackingScaleEnabled(), expectedWidth, expectedHeight, reportedExpectedWidth, reportedExpectedHeight)
}

func (v *View) traceScreenGeometryCallback(callback string, viewX, viewY int32, returns bool) {
	if !traceOSREnabled() || v == nil || v.osrTraceCount.Add(1) > 96 {
		return
	}
	widgetWidth, widgetHeight := v.cachedSize()
	osrWidth, osrHeight := v.osrViewRectSize()
	scale := float64(v.DeviceScaleFactor())
	expectedWidth, expectedHeight := expectedDeviceSize(widgetWidth, widgetHeight, scale)
	fmt.Fprintf(os.Stderr,
		"cef2gtk-osr-geometry callback=%s backend=%s widget_logical=%dx%d osr_rect=%dx%d view_point=%d,%d device_scale=%.3f expected_device=%dx%d returns=%t\n",
		callback, v.backend.String(), widgetWidth, widgetHeight, osrWidth, osrHeight, viewX, viewY, scale, expectedWidth, expectedHeight, returns)
}

func expectedDeviceSize(width, height int32, scale float64) (int32, int32) {
	return int32(math.Ceil(float64(width) * scale)), int32(math.Ceil(float64(height) * scale))
}

// AddSizeObserver registers a callback invoked on the GTK thread when the view
// observes a positive size change. It returns a function that unregisters the
// observer. If a real positive size has already been observed, the callback is
// invoked immediately with that size; the synthetic Size() fallback is not
// emitted as an observer event. Register and unregister from the GTK thread.
func (v *View) AddSizeObserver(fn func(width, height int32)) func() {
	if v == nil || fn == nil {
		return func() {}
	}
	v.sizeHooksMu.Lock()
	if v.sizeHooks == nil {
		v.sizeHooks = make(map[uint64]func(width, height int32))
	}
	v.nextSizeHookID++
	id := v.nextSizeHookID
	v.sizeHooks[id] = fn
	v.sizeHooksMu.Unlock()
	width, height, ok := v.observedSize()
	if ok {
		fn(width, height)
	}
	return func() {
		v.sizeHooksMu.Lock()
		delete(v.sizeHooks, id)
		v.sizeHooksMu.Unlock()
	}
}

func (v *View) emitSizeHooks(width, height int32) {
	v.sizeHooksMu.Lock()
	hooks := make([]func(width, height int32), 0, len(v.sizeHooks))
	for _, hook := range v.sizeHooks {
		hooks = append(hooks, hook)
	}
	v.sizeHooksMu.Unlock()
	for _, hook := range hooks {
		hook(width, height)
	}
}

func (v *View) renderOnGTKThread() bool {
	if v == nil || v.renderer == nil {
		return false
	}
	if err := v.renderer.RenderQueuedOnGTKThread(); err != nil {
		v.diag.RecordRenderFailure(err)
		v.recordProfileRenderFailure()
		if v.hooks.OnError != nil {
			v.hooks.OnError(err)
		}
		v.emitProfileIfDue(time.Now())
		return false
	}
	v.recordProfileFrameRendered()
	v.emitProfileIfDue(time.Now())
	return true
}

// HasFocus reports whether the underlying GTK widget currently has focus.
// Call on the GTK main thread.
func (v *View) HasFocus() bool {
	if v == nil || v.widget == nil {
		return false
	}
	return v.widget.HasFocus()
}

// SetCursorFromName applies a named cursor to the underlying GTK widget. Call
// on the GTK main thread.
func (v *View) SetCursorFromName(name string) {
	if v == nil || v.widget == nil {
		return
	}
	v.widget.SetCursorFromName(&name)
}

// Widget returns the GtkWidget for packing into GTK containers.
func (v *View) Widget() *gtk.Widget {
	if v == nil {
		return nil
	}
	return v.widget
}

// Backend returns the selected presentation backend.
func (v *View) Backend() Backend {
	if v == nil {
		return ""
	}
	return v.backend
}

// GLArea returns the underlying GtkGLArea for the GLArea backend.
func (v *View) GLArea() *gtk.GLArea {
	if v == nil {
		return nil
	}
	return v.area
}

// PrepareOnGTKThread initializes renderer resources. Call on GTK main thread.
func (v *View) PrepareOnGTKThread() error {
	if v == nil || v.renderer == nil {
		return ErrNilView
	}
	if v.area == nil {
		return v.renderer.InitializeOnGTKThread()
	}
	if !v.area.GetRealized() {
		return gtkgl.ErrGLAreaNotRealized
	}
	v.area.MakeCurrent()
	return v.renderer.InitializeOnGTKThread()
}

// ConfigureProfiling enables or disables development-only render profiling.
// When enabled, snapshots are emitted at opts.Interval through opts.OnSnapshot
// and/or opts.Writer. Rendering continues if snapshot writing fails.
func (v *View) ConfigureProfiling(opts ProfileOptions) error {
	if v == nil || v.renderer == nil {
		return ErrNilView
	}
	v.profileMu.Lock()
	defer v.profileMu.Unlock()
	if !opts.Enabled {
		v.profileEnabled.Store(false)
		v.profile = nil
		v.profileOptions = ProfileOptions{}
		v.renderer.SetProfiler(nil)
		return nil
	}
	recorder := internalprofile.NewRecorder()
	recorder.SetBackend(v.backend.String())
	recorder.Start(time.Now())
	v.profile = recorder
	v.profileOptions = opts
	v.profileEnabled.Store(true)
	v.renderer.SetProfiler(recorder)
	return nil
}

func (v *View) profileRecorder() *internalprofile.Recorder {
	if v == nil || !v.profileEnabled.Load() {
		return nil
	}
	v.profileMu.Lock()
	defer v.profileMu.Unlock()
	return v.profile
}

func (v *View) recordProfileFrameReceived() {
	if p := v.profileRecorder(); p != nil {
		p.RecordFrameReceived()
	}
}

func (v *View) recordProfileFrameQueued() {
	if p := v.profileRecorder(); p != nil {
		p.RecordFrameQueued()
	}
}

func (v *View) recordProfileFrameRendered() {
	if p := v.profileRecorder(); p != nil {
		p.RecordFrameRendered()
	}
}

func (v *View) recordProfileImportFailure() {
	if p := v.profileRecorder(); p != nil {
		p.RecordImportFailure()
	}
}

func (v *View) recordProfileRenderFailure() {
	if p := v.profileRecorder(); p != nil {
		p.RecordRenderFailure()
	}
}

func (v *View) recordProfileUnsupportedPaint() {
	if p := v.profileRecorder(); p != nil {
		p.RecordUnsupportedPaint()
	}
}

func (v *View) emitProfileIfDue(now time.Time) {
	if v == nil || !v.profileEnabled.Load() {
		return
	}
	v.profileMu.Lock()
	p := v.profile
	opts := v.profileOptions
	v.profileMu.Unlock()
	if p == nil || !opts.Enabled {
		return
	}
	snap, ok := p.MaybeSnapshot(now, opts.normalizedInterval())
	if !ok {
		return
	}
	onSnapshot := opts.OnSnapshot
	writer := opts.Writer
	if onSnapshot != nil {
		onSnapshot(snap)
	}
	_ = writeProfileSnapshot(writer, snap)
}

// Diagnostics returns a point-in-time diagnostics snapshot.
func (v *View) Diagnostics() Diagnostics {
	if v == nil || v.diag == nil {
		return Diagnostics{}
	}
	snap := v.diag.Snapshot()
	snap.Backend = v.backend.String()
	if gdkDiag, ok := v.renderer.(interface{ Diagnostics() gtkgdk.Diagnostics }); ok {
		d := gdkDiag.Diagnostics()
		snap.TexturesBuilt = int(d.TexturesBuilt)
		snap.TextureBuildFailures = int(d.TextureBuildFailures)
		snap.FDDupFailures = int(d.FDDupFailures)
		snap.UnsupportedFormats = int(d.UnsupportedFormats)
		snap.PaintableSwaps = int(d.PaintableSwaps)
		snap.PendingFrame = d.PendingFrame
		snap.PendingScheduled = d.PendingScheduled
		snap.PendingAge = d.PendingAge
		snap.PendingSourceID = d.PendingSourceID
		snap.PendingReschedules = int(d.PendingReschedules)
		snap.PendingScheduleFailures = int(d.PendingScheduleFailures)
		snap.PendingIdleCallbacks = int(d.PendingIdleCallbacks)
	}
	return snap
}

// Destroy releases GL resources owned by the view. Call on the GTK main thread;
// it disconnects GTK signal handlers and is not safe for concurrent use.
func (v *View) Destroy() error {
	if v == nil {
		return ErrNilView
	}
	if v.input != nil {
		v.input.Detach()
		v.input = nil
	}
	if v.widget != nil && v.sizeTickID != 0 {
		v.widget.RemoveTickCallback(v.sizeTickID)
		v.sizeTickID = 0
		v.sizeTickFunc = nil
	}
	if v.signalObject != nil {
		if v.widthHandlerID != 0 {
			gobject.SignalHandlerDisconnect(v.signalObject, v.widthHandlerID)
			v.widthHandlerID = 0
		}
		if v.heightHandlerID != 0 {
			gobject.SignalHandlerDisconnect(v.signalObject, v.heightHandlerID)
			v.heightHandlerID = 0
		}
		if v.unrealizeHandlerID != 0 {
			gobject.SignalHandlerDisconnect(v.signalObject, v.unrealizeHandlerID)
			v.unrealizeHandlerID = 0
		}
		v.signalObject = nil
	}
	if v.area != nil && v.renderHandlerID != 0 {
		gobject.SignalHandlerDisconnect(&v.area.Object, v.renderHandlerID)
		v.renderHandlerID = 0
	}
	if v.renderer != nil {
		if v.area != nil {
			if v.area.GetRealized() {
				v.area.MakeCurrent()
				v.renderer.Close()
			} else {
				v.renderer.InvalidateOnGTKThread()
			}
		} else {
			v.renderer.InvalidateOnGTKThread()
		}
		v.renderer = nil
	}
	v.sizeHooksMu.Lock()
	v.sizeHooks = nil
	v.sizeHooksMu.Unlock()
	v.profileMu.Lock()
	v.profileEnabled.Store(false)
	v.profile = nil
	v.profileOptions = ProfileOptions{}
	v.profileMu.Unlock()
	v.handler = nil
	v.area = nil
	v.widget = nil
	return nil
}
