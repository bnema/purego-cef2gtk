package gtkgdk

import (
	"math"
	"structs"
	"unsafe"

	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
	"github.com/bnema/puregotk/v4/gtk"
)

// cRectangle mirrors C GdkRectangle (four C ints). puregotk's gdk.Rectangle
// uses Go int fields, which do not match the C layout on 64-bit targets.
type cRectangle struct {
	_      structs.HostLayout
	X      int32
	Y      int32
	Width  int32
	Height int32
}

// letterboxAllocation returns where the presenter must be placed inside a
// widthxheight clipping parent so only crop of a coded-size frame is visible.
// The presenter is scaled up so crop fills the parent, and shifted so crop's
// origin lands at (0,0); the clipping parent hides the letterbox bars.
func letterboxAllocation(width, height int, coded dmabuf.Size, crop dmabuf.Rect) cRectangle {
	scaleX := float64(width) / float64(crop.Width)
	scaleY := float64(height) / float64(crop.Height)
	return cRectangle{
		X:      -int32(math.Round(float64(crop.X) * scaleX)),
		Y:      -int32(math.Round(float64(crop.Y) * scaleY)),
		Width:  int32(math.Round(float64(coded.Width) * scaleX)),
		Height: int32(math.Round(float64(coded.Height) * scaleY)),
	}
}

// letterboxClip wraps the presenter in a clipping GtkOverlay whose
// get-child-position handler enlarges and offsets the presenter while CEF's
// capturer delivers letterboxed frames after a resize.
type letterboxClip struct {
	overlay   *gtk.Overlay
	presenter *gtk.Widget
	position  func(gtk.Overlay, uintptr, *uintptr) bool

	coded  dmabuf.Size
	crop   dmabuf.Rect
	active bool
}

func newLetterboxClip(presenter *gtk.Widget) *letterboxClip {
	overlay := gtk.NewOverlay()
	if overlay == nil || presenter == nil {
		return nil
	}
	c := &letterboxClip{overlay: overlay, presenter: presenter}
	overlay.SetOverflow(gtk.OverflowHiddenValue)
	overlay.SetHexpand(true)
	overlay.SetVexpand(true)
	overlay.SetSizeRequest(1, 1)
	overlay.AddOverlay(presenter)
	overlay.SetMeasureOverlay(presenter, true)
	overlay.SetClipOverlay(presenter, true)
	c.position = func(_ gtk.Overlay, _ uintptr, allocation *uintptr) bool {
		if allocation == nil {
			return false
		}
		rect := (*cRectangle)(unsafe.Pointer(allocation))
		width, height := overlay.GetWidth(), overlay.GetHeight()
		if !c.active || width <= 0 || height <= 0 {
			*rect = cRectangle{Width: int32(max(width, 1)), Height: int32(max(height, 1))}
			return true
		}
		*rect = letterboxAllocation(width, height, c.coded, c.crop)
		return true
	}
	overlay.ConnectGetChildPosition(&c.position)
	return c
}

// Widget returns the clipping container to pack instead of the presenter.
func (c *letterboxClip) Widget() *gtk.Widget {
	if c == nil {
		return nil
	}
	return &c.overlay.Widget
}

// Update records the content rect of the frame being presented and
// re-allocates only when the crop changes. Call on the GTK thread.
func (c *letterboxClip) Update(coded dmabuf.Size, content dmabuf.Rect) {
	if c == nil {
		return
	}
	crop, active := dmabuf.LetterboxCrop(coded, content)
	if active == c.active && (!active || (crop == c.crop && coded == c.coded)) {
		return
	}
	c.active, c.coded, c.crop = active, coded, crop
	c.overlay.QueueAllocate()
}
