package cefadapter

import (
	"errors"
	"reflect"
	"testing"

	"github.com/bnema/purego-cef/cef"

	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
)

const (
	testCodedWidth        int32  = 641
	testCodedHeight       int32  = 481
	testVisibleX          int32  = 11
	testVisibleY          int32  = 13
	testVisibleWidth      int32  = 617
	testVisibleHeight     int32  = 457
	testContentX          int32  = 17
	testContentY          int32  = 19
	testContentWidth      int32  = 593
	testContentHeight     int32  = 431
	testSourceWidth       int32  = 701
	testSourceHeight      int32  = 523
	testModifier          uint64 = 0x1020304050607080
	testPlaneFD           int32  = 23
	testPlaneStride       uint32 = 4096
	testPlaneOffset       uint64 = 128
	testPlaneSize         uint64 = 1 << 20
	testSecondPlaneFD     int32  = 24
	testSecondPlaneStride uint32 = 2048
	testBadFormat         int32  = 0x3231564e // NV12, intentionally unsupported in phase 2.
)

func validAcceleratedPaintInfo() cef.AcceleratedPaintInfo {
	info := cef.NewAcceleratedPaintInfo()
	info.PlaneCount = 1
	info.Format = int32(dmabuf.FormatARGB8888)
	info.Modifier = testModifier
	info.Planes[0].Fd = testPlaneFD
	info.Planes[0].Stride = testPlaneStride
	info.Planes[0].Offset = testPlaneOffset
	info.Planes[0].Size = testPlaneSize
	info.Extra.CodedSize.Width = testCodedWidth
	info.Extra.CodedSize.Height = testCodedHeight
	info.Extra.VisibleRect.X = testVisibleX
	info.Extra.VisibleRect.Y = testVisibleY
	info.Extra.VisibleRect.Width = testVisibleWidth
	info.Extra.VisibleRect.Height = testVisibleHeight
	info.Extra.ContentRect.X = testContentX
	info.Extra.ContentRect.Y = testContentY
	info.Extra.ContentRect.Width = testContentWidth
	info.Extra.ContentRect.Height = testContentHeight
	info.Extra.SourceSize.Width = testSourceWidth
	info.Extra.SourceSize.Height = testSourceHeight
	return info
}

func TestBorrowedFrameFromAcceleratedPaintMapsFieldsExactly(t *testing.T) {
	info := validAcceleratedPaintInfo()

	got, err := BorrowedFrameFromAcceleratedPaint(&info)
	if err != nil {
		t.Fatalf("BorrowedFrameFromAcceleratedPaint error = %v", err)
	}

	want := dmabuf.BorrowedFrame{
		CodedSize:   dmabuf.Size{Width: testCodedWidth, Height: testCodedHeight},
		VisibleRect: dmabuf.Rect{X: testVisibleX, Y: testVisibleY, Width: testVisibleWidth, Height: testVisibleHeight},
		ContentRect: dmabuf.Rect{X: testContentX, Y: testContentY, Width: testContentWidth, Height: testContentHeight},
		SourceSize:  dmabuf.Size{Width: testSourceWidth, Height: testSourceHeight},
		Format:      dmabuf.FormatARGB8888,
		Modifier:    testModifier,
		Planes: []dmabuf.Plane{{
			FD:     int(testPlaneFD),
			Stride: testPlaneStride,
			Offset: testPlaneOffset,
			Size:   testPlaneSize,
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BorrowedFrameFromAcceleratedPaint() = %+v, want %+v", got, want)
	}
}

func TestBorrowedFrameFromAcceleratedPaintRejectsNilInfo(t *testing.T) {
	if _, err := BorrowedFrameFromAcceleratedPaint(nil); !errors.Is(err, ErrNilAcceleratedPaintInfo) {
		t.Fatalf("BorrowedFrameFromAcceleratedPaint(nil) error = %v, want %v", err, ErrNilAcceleratedPaintInfo)
	}
}

func TestBorrowedFrameFromAcceleratedPaintRejectsPlaneCountBounds(t *testing.T) {
	for _, planeCount := range []int32{0, -1, 5} {
		info := validAcceleratedPaintInfo()
		info.PlaneCount = planeCount
		if _, err := BorrowedFrameFromAcceleratedPaint(&info); !errors.Is(err, ErrInvalidPlaneCount) {
			t.Fatalf("BorrowedFrameFromAcceleratedPaint plane count %d error = %v, want %v", planeCount, err, ErrInvalidPlaneCount)
		}
	}
}

func TestBorrowedFrameFromAcceleratedPaintPropagatesUnsupportedPlanesValidation(t *testing.T) {
	info := validAcceleratedPaintInfo()
	info.PlaneCount = 2
	info.Planes[1].Fd = testSecondPlaneFD
	info.Planes[1].Stride = testSecondPlaneStride

	if _, err := BorrowedFrameFromAcceleratedPaint(&info); !errors.Is(err, dmabuf.ErrUnsupportedPlanes) {
		t.Fatalf("BorrowedFrameFromAcceleratedPaint multi-plane error = %v, want %v", err, dmabuf.ErrUnsupportedPlanes)
	}
}

func TestBorrowedFrameFromAcceleratedPaintPropagatesUnsupportedFormatValidation(t *testing.T) {
	info := validAcceleratedPaintInfo()
	info.Format = testBadFormat

	if _, err := BorrowedFrameFromAcceleratedPaint(&info); !errors.Is(err, dmabuf.ErrUnsupportedFormat) {
		t.Fatalf("BorrowedFrameFromAcceleratedPaint bad format error = %v, want %v", err, dmabuf.ErrUnsupportedFormat)
	}
}
