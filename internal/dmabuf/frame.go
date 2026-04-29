package dmabuf

import (
	"errors"
	"fmt"
)

const supportedPlaneCount = 1

var (
	ErrInvalidCodedSize  = errors.New("invalid coded size")
	ErrUnsupportedFormat = errors.New("unsupported dmabuf format")
	ErrUnsupportedPlanes = errors.New("unsupported dmabuf plane count")
	ErrInvalidPlaneFD    = errors.New("invalid dmabuf plane fd")
	ErrInvalidStride     = errors.New("invalid dmabuf plane stride")
)

// Size describes pixel dimensions.
type Size struct {
	Width  int32
	Height int32
}

// Valid reports whether both dimensions are positive.
func (s Size) Valid() bool { return s.Width > 0 && s.Height > 0 }

// Rect describes a pixel rectangle.
type Rect struct {
	X      int32
	Y      int32
	Width  int32
	Height int32
}

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool { return r.Width == 0 || r.Height == 0 }

// Plane describes one borrowed native pixmap plane. The FD remains owned by CEF.
type Plane struct {
	FD     int
	Stride uint32
	Offset uint64
	Size   uint64
}

// BorrowedFrame describes a callback-scoped DMABUF frame. It must be imported
// and copied before the originating CEF callback returns.
type BorrowedFrame struct {
	CodedSize Size

	VisibleRect Rect
	ContentRect Rect
	SourceSize  Size

	Format   FourCC
	Modifier uint64
	Planes   []Plane
}

// Validate enforces the intentionally small initial support matrix: exactly one
// plane, one of four RGB formats, positive coded size, valid fd, and non-zero stride.
func (f BorrowedFrame) Validate() error {
	if !f.CodedSize.Valid() {
		return fmt.Errorf("%w: %dx%d", ErrInvalidCodedSize, f.CodedSize.Width, f.CodedSize.Height)
	}
	if !f.Format.Supported() {
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, f.Format)
	}
	if len(f.Planes) != supportedPlaneCount {
		return fmt.Errorf("%w: got %d, want %d", ErrUnsupportedPlanes, len(f.Planes), supportedPlaneCount)
	}
	plane := f.Planes[0]
	if plane.FD <= 0 {
		return fmt.Errorf("%w: %d", ErrInvalidPlaneFD, plane.FD)
	}
	if plane.Stride == 0 {
		return fmt.Errorf("%w: %d", ErrInvalidStride, plane.Stride)
	}
	return nil
}
