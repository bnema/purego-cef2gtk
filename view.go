package cef2gtk

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgdk"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
	internalprofile "github.com/bnema/purego-cef2gtk/internal/profile"
	"github.com/bnema/puregotk/v4/gobject"
	"github.com/bnema/puregotk/v4/gtk"
)

var ErrNilView = errors.New("nil View")

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
	inputScale         int32
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
	scale              atomic.Int32
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
	if scale := int32(v.widget.GetScaleFactor()); scale > 0 {
		v.scale.Store(scale)
	}
	if changed && width > 0 && height > 0 {
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

// DeviceScaleFactor returns the last GTK scale factor observed for the view.
// Values <= 0 are normalized to 1.
func (v *View) DeviceScaleFactor() float32 {
	if v == nil {
		return 1
	}
	scale := v.scale.Load()
	if scale <= 0 {
		scale = 1
	}
	return float32(scale)
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
