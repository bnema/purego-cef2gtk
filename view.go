package cef2gtk

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
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

type acceleratedRenderer interface {
	InitializeOnGTKThread() error
	ImportCopyAndQueueOnGTKThread(*cef.AcceleratedPaintInfo) (gtkgl.QueuedFrame, error)
	QueueRender()
	RenderQueuedOnGTKThread() error
	Close()
}

// View is a GTK GtkGLArea-backed CEF OSR view.
type View struct {
	area            *gtk.GLArea
	renderer        acceleratedRenderer
	input           *gtkgl.InputBridge
	inputScale      int32
	diag            *diagnosticsRecorder
	hooks           Hooks
	handler         *renderHandler
	renderFunc      func(gtk.GLArea, uintptr) bool
	renderHandlerID uint
	widthNotify     func(gobject.Object, *gobject.ParamSpec)
	heightNotify    func(gobject.Object, *gobject.ParamSpec)
	widthHandlerID  uint
	heightHandlerID uint
	cachedWidth     atomic.Int32
	cachedHeight    atomic.Int32
	scale           atomic.Int32
	sizeHooksMu     sync.Mutex
	sizeHooks       map[uint64]func(width, height int32)
	nextSizeHookID  uint64
}

// NewView creates a GtkGLArea-backed accelerated CEF view.
func NewView() *View {
	area := gtk.NewGLArea()
	if area == nil {
		return nil
	}
	v := &View{area: area, diag: newDiagnosticsRecorder()}
	area.SetAutoRender(false)
	v.renderer = gtkgl.NewAcceleratedRenderer(area)
	v.connectRenderSignal()
	return v
}

func (v *View) connectRenderSignal() {
	if v == nil || v.area == nil || v.renderer == nil {
		return
	}
	v.updateCachedSizeOnGTKThread()
	v.widthNotify = func(gobject.Object, *gobject.ParamSpec) { v.updateCachedSizeOnGTKThread() }
	v.heightNotify = func(gobject.Object, *gobject.ParamSpec) { v.updateCachedSizeOnGTKThread() }
	v.widthHandlerID = v.area.ConnectNotifyWithDetail("width", &v.widthNotify)
	v.heightHandlerID = v.area.ConnectNotifyWithDetail("height", &v.heightNotify)
	v.renderFunc = func(_ gtk.GLArea, _ uintptr) bool {
		v.updateCachedSizeOnGTKThread()
		return v.renderOnGTKThread()
	}
	v.renderHandlerID = v.area.ConnectRender(&v.renderFunc)
}

func (v *View) updateCachedSizeOnGTKThread() {
	if v == nil || v.area == nil {
		return
	}
	width := int32(v.area.GetWidth())
	height := int32(v.area.GetHeight())
	changed := false
	if width > 0 {
		changed = v.cachedWidth.Swap(width) != width || changed
	}
	if height > 0 {
		changed = v.cachedHeight.Swap(height) != height || changed
	}
	if scale := int32(v.area.GetScaleFactor()); scale > 0 {
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
// observer. Register and unregister from the GTK thread.
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
	width, height := v.Size()
	if width > 1 || height > 1 {
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
		if v.hooks.OnError != nil {
			v.hooks.OnError(err)
		}
		return false
	}
	return true
}

// HasFocus reports whether the underlying GTK widget currently has focus.
// Call on the GTK main thread.
func (v *View) HasFocus() bool {
	if v == nil || v.area == nil {
		return false
	}
	return v.area.HasFocus()
}

// SetCursorFromName applies a named cursor to the underlying GTK widget. Call
// on the GTK main thread.
func (v *View) SetCursorFromName(name string) {
	if v == nil || v.area == nil {
		return
	}
	v.area.SetCursorFromName(&name)
}

// Widget returns the GtkWidget for packing into GTK containers.
func (v *View) Widget() *gtk.Widget {
	if v == nil || v.area == nil {
		return nil
	}
	return &v.area.Widget
}

// GLArea returns the underlying GtkGLArea.
func (v *View) GLArea() *gtk.GLArea {
	if v == nil {
		return nil
	}
	return v.area
}

// PrepareOnGTKThread initializes renderer GL/EGL resources. Call on GTK main thread.
func (v *View) PrepareOnGTKThread() error {
	if v == nil || v.renderer == nil || v.area == nil {
		return ErrNilView
	}
	v.area.MakeCurrent()
	return v.renderer.InitializeOnGTKThread()
}

// Diagnostics returns a point-in-time diagnostics snapshot.
func (v *View) Diagnostics() Diagnostics {
	if v == nil || v.diag == nil {
		return Diagnostics{}
	}
	return v.diag.Snapshot()
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
	if v.area != nil {
		obj := &v.area.Object
		if v.renderHandlerID != 0 {
			gobject.SignalHandlerDisconnect(obj, v.renderHandlerID)
			v.renderHandlerID = 0
		}
		if v.widthHandlerID != 0 {
			gobject.SignalHandlerDisconnect(obj, v.widthHandlerID)
			v.widthHandlerID = 0
		}
		if v.heightHandlerID != 0 {
			gobject.SignalHandlerDisconnect(obj, v.heightHandlerID)
			v.heightHandlerID = 0
		}
	}
	if v.renderer != nil {
		if v.area != nil {
			v.area.MakeCurrent()
		}
		v.renderer.Close()
		v.renderer = nil
	}
	v.sizeHooksMu.Lock()
	v.sizeHooks = nil
	v.sizeHooksMu.Unlock()
	v.handler = nil
	v.area = nil
	return nil
}
