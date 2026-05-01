package cef2gtk

import (
	"errors"
	"time"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
)

type renderQueue interface {
	ImportAndQueueOnGTKThread(*cef.AcceleratedPaintInfo) (gtkgl.QueuedFrame, error)
	QueueRender()
}

type asyncRenderQueue interface {
	ImportAndQueueAsync(*cef.AcceleratedPaintInfo, func(error)) error
}

type renderHandler struct {
	view        *View
	renderer    renderQueue
	diag        *diagnosticsRecorder
	staticHooks Hooks
}

var _ cef.RenderHandler = (*renderHandler)(nil)

// RenderHandler returns a CEF render handler adapter for this view.
func (v *View) RenderHandler(hooks Hooks) cef.RenderHandler {
	if v == nil {
		return &renderHandler{staticHooks: hooks}
	}
	v.hooks = hooks
	if v.handler != nil {
		return v.handler
	}
	v.handler = &renderHandler{view: v, renderer: v.renderer, diag: v.diag}
	return v.handler
}

func (h *renderHandler) GetAccessibilityHandler() cef.AccessibilityHandler { return nil }
func (h *renderHandler) GetRootScreenRect(cef.Browser, *cef.Rect) int32    { return 0 }
func (h *renderHandler) GetViewRect(_ cef.Browser, rect *cef.Rect) {
	if rect == nil {
		return
	}
	rect.X, rect.Y = 0, 0
	if h.view == nil {
		rect.Width, rect.Height = 1, 1
		return
	}
	rect.Width, rect.Height = h.view.cachedSize()
}
func (h *renderHandler) GetScreenPoint(cef.Browser, int32, int32, *int32, *int32) int32 { return 0 }
func (h *renderHandler) GetScreenInfo(_ cef.Browser, info *cef.ScreenInfo) int32 {
	if info == nil || h.view == nil {
		return 0
	}
	width, height := h.view.cachedSize()
	rect := cef.Rect{X: 0, Y: 0, Width: width, Height: height}
	si := cef.NewScreenInfo()
	si.DeviceScaleFactor = h.view.DeviceScaleFactor()
	si.Depth = 24
	si.DepthPerComponent = 8
	si.Rect = rect
	si.AvailableRect = rect
	*info = si
	return 1
}
func (h *renderHandler) OnPopupShow(cef.Browser, int32)     {}
func (h *renderHandler) OnPopupSize(cef.Browser, *cef.Rect) {}
func (h *renderHandler) OnPaint(cef.Browser, cef.PaintElementType, []cef.Rect, []byte, int32, int32) {
	if h.diag != nil {
		h.diag.RecordUnsupportedPaint()
	}
	if h.view != nil {
		h.view.recordProfileUnsupportedPaint()
		h.view.emitProfileIfDue(time.Now())
	}
	hooks := h.hooks()
	if hooks.OnUnsupportedPaint != nil {
		hooks.OnUnsupportedPaint()
	}
}
func (h *renderHandler) OnAcceleratedPaint(_ cef.Browser, _ cef.PaintElementType, _ []cef.Rect, info *cef.AcceleratedPaintInfo) {
	if h.diag != nil {
		h.diag.RecordAcceleratedPaint()
	}
	if h.view != nil {
		h.view.recordProfileFrameReceived()
	}
	if h.renderer == nil {
		h.handleAcceleratedError(gtkgl.ErrNilAcceleratedRenderer)
		return
	}
	if async, ok := h.renderer.(asyncRenderQueue); ok {
		if err := async.ImportAndQueueAsync(info, h.handleAcceleratedError); err != nil {
			h.handleAcceleratedError(err)
			return
		}
		h.recordAcceleratedFrameQueued()
		return
	}
	if _, err := h.renderer.ImportAndQueueOnGTKThread(info); err != nil {
		h.handleAcceleratedError(err)
		return
	}
	h.recordAcceleratedFrameQueued()
}

func (h *renderHandler) recordAcceleratedFrameQueued() {
	h.renderer.QueueRender()
	if h.view != nil {
		h.view.recordProfileFrameQueued()
		h.view.emitProfileIfDue(time.Now())
	}
}
func (h *renderHandler) handleAcceleratedError(err error) {
	if h.diag != nil {
		h.diag.RecordImportFailure(err)
	}
	if h.view != nil {
		h.view.recordProfileImportFailure()
		h.view.emitProfileIfDue(time.Now())
	}
	if isTransientGLAreaLifecycleError(err) {
		return
	}
	hooks := h.hooks()
	if hooks.OnError != nil {
		hooks.OnError(err)
	}
}

func isTransientGLAreaLifecycleError(err error) bool {
	return errors.Is(err, gtkgl.ErrGLAreaNotRealized) || errors.Is(err, gtkgl.ErrMissingGLAreaContext)
}
func (h *renderHandler) hooks() Hooks {
	if h != nil && h.view != nil {
		return h.view.hooks
	}
	if h != nil {
		return h.staticHooks
	}
	return Hooks{}
}

func (h *renderHandler) GetTouchHandleSize(cef.Browser, cef.HorizontalAlignment, *cef.Size) {}
func (h *renderHandler) OnTouchHandleStateChanged(cef.Browser, *cef.TouchHandleState)       {}
func (h *renderHandler) StartDragging(cef.Browser, cef.DragData, cef.DragOperationsMask, int32, int32) int32 {
	return 0
}
func (h *renderHandler) UpdateDragCursor(cef.Browser, cef.DragOperationsMask)             {}
func (h *renderHandler) OnScrollOffsetChanged(cef.Browser, float64, float64)              {}
func (h *renderHandler) OnImeCompositionRangeChanged(cef.Browser, *cef.Range, []cef.Rect) {}
func (h *renderHandler) OnTextSelectionChanged(_ cef.Browser, selectedText string, selectedRange *cef.Range) {
	hooks := h.hooks()
	if hooks.OnTextSelectionChanged != nil {
		hooks.OnTextSelectionChanged(selectedText, selectedRange)
	}
}
func (h *renderHandler) OnVirtualKeyboardRequested(cef.Browser, cef.TextInputMode) {}
