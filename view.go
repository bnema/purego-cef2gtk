package cef2gtk

import (
	"errors"
	"sync/atomic"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
	"github.com/bnema/puregotk/v4/gobject"
	"github.com/bnema/puregotk/v4/gtk"
)

var ErrNilView = errors.New("nil View")

// Hooks contains optional callbacks invoked by the public CEF render adapter.
type Hooks struct {
	OnUnsupportedPaint func()
	OnError            func(error)
}

type acceleratedRenderer interface {
	InitializeOnGTKThread() error
	ImportCopyAndQueue(*cef.AcceleratedPaintInfo) (gtkgl.QueuedFrame, error)
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
	if width > 0 {
		v.cachedWidth.Store(width)
	}
	if height > 0 {
		v.cachedHeight.Store(height)
	}
}

func (v *View) cachedSize() (int32, int32) {
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
	if v == nil || v.renderer == nil {
		return ErrNilView
	}
	return v.renderer.InitializeOnGTKThread()
}

// Diagnostics returns a point-in-time diagnostics snapshot.
func (v *View) Diagnostics() Diagnostics {
	if v == nil || v.diag == nil {
		return Diagnostics{}
	}
	return v.diag.Snapshot()
}

// Destroy releases GL resources owned by the view.
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
		v.renderer.Close()
		v.renderer = nil
	}
	v.handler = nil
	v.area = nil
	return nil
}
