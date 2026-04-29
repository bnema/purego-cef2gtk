package cef2gtk

import (
	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
)

type acceleratedRenderQueue interface {
	ImportCopyAndQueue(*cef.AcceleratedPaintInfo) (gtkgl.QueuedFrame, error)
	QueueRender()
}

type renderHandler struct {
	view     *View
	renderer acceleratedRenderQueue
	diag     *diagnosticsRecorder
	hooks    Hooks
}

var _ cef.RenderHandler = (*renderHandler)(nil)

// RenderHandler returns a CEF render handler adapter for this view.
func (v *View) RenderHandler(hooks Hooks) cef.RenderHandler {
	if v == nil {
		return &renderHandler{hooks: hooks}
	}
	if v.handler != nil {
		v.hooks = hooks
		v.handler.hooks = hooks
		return v.handler
	}
	v.hooks = hooks
	v.handler = &renderHandler{view: v, renderer: v.renderer, diag: v.diag, hooks: hooks}
	return v.handler
}

func (h *renderHandler) GetAccessibilityHandler() cef.AccessibilityHandler { return nil }
func (h *renderHandler) GetRootScreenRect(cef.Browser, *cef.Rect) int32    { return 0 }
func (h *renderHandler) GetViewRect(_ cef.Browser, rect *cef.Rect) {
	if rect == nil {
		return
	}
	if h.view != nil {
		rect.Width, rect.Height = h.view.cachedSize()
	}
	if rect.Width <= 0 {
		rect.Width = 1
	}
	if rect.Height <= 0 {
		rect.Height = 1
	}
}
func (h *renderHandler) GetScreenPoint(cef.Browser, int32, int32, *int32, *int32) int32 { return 0 }
func (h *renderHandler) GetScreenInfo(cef.Browser, *cef.ScreenInfo) int32               { return 0 }
func (h *renderHandler) OnPopupShow(cef.Browser, int32)                                 {}
func (h *renderHandler) OnPopupSize(cef.Browser, *cef.Rect)                             {}
func (h *renderHandler) OnPaint(cef.Browser, cef.PaintElementType, []cef.Rect, []byte, int32, int32) {
	if h.diag != nil {
		h.diag.RecordUnsupportedPaint()
	}
	if h.hooks.OnUnsupportedPaint != nil {
		h.hooks.OnUnsupportedPaint()
	}
}
func (h *renderHandler) OnAcceleratedPaint(_ cef.Browser, _ cef.PaintElementType, _ []cef.Rect, info *cef.AcceleratedPaintInfo) {
	if h.diag != nil {
		h.diag.RecordAcceleratedPaint()
	}
	if h.renderer == nil {
		h.handleAcceleratedError(gtkgl.ErrNilAcceleratedRenderer)
		return
	}
	if _, err := h.renderer.ImportCopyAndQueue(info); err != nil {
		h.handleAcceleratedError(err)
		return
	}
	h.renderer.QueueRender()
}
func (h *renderHandler) handleAcceleratedError(err error) {
	if h.diag != nil {
		h.diag.RecordImportFailure(err)
	}
	if h.hooks.OnError != nil {
		h.hooks.OnError(err)
	}
}
func (h *renderHandler) GetTouchHandleSize(cef.Browser, cef.HorizontalAlignment, *cef.Size) {}
func (h *renderHandler) OnTouchHandleStateChanged(cef.Browser, *cef.TouchHandleState)       {}
func (h *renderHandler) StartDragging(cef.Browser, cef.DragData, cef.DragOperationsMask, int32, int32) int32 {
	return 0
}
func (h *renderHandler) UpdateDragCursor(cef.Browser, cef.DragOperationsMask)             {}
func (h *renderHandler) OnScrollOffsetChanged(cef.Browser, float64, float64)              {}
func (h *renderHandler) OnImeCompositionRangeChanged(cef.Browser, *cef.Range, []cef.Rect) {}
func (h *renderHandler) OnTextSelectionChanged(cef.Browser, string, *cef.Range)           {}
func (h *renderHandler) OnVirtualKeyboardRequested(cef.Browser, cef.TextInputMode)        {}
