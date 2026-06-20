package dmabuf

import (
	"errors"
	"math"
	"testing"
)

const (
	testCodedWidth  int32  = 640
	testCodedHeight int32  = 480
	testPlaneFD     int    = 7
	testStride      uint32 = 2560
)

func validFrame() BorrowedFrame {
	return BorrowedFrame{
		CodedSize: Size{Width: testCodedWidth, Height: testCodedHeight},
		Format:    FormatARGB8888,
		Modifier:  ModifierInvalid,
		Planes: []Plane{{
			FD:     testPlaneFD,
			Stride: testStride,
		}},
	}
}

func TestBorrowedFrameValidateAcceptsSupportedOnePlaneRGBFormats(t *testing.T) {
	for _, format := range []FourCC{FormatARGB8888, FormatXRGB8888, FormatABGR8888, FormatXBGR8888} {
		frame := validFrame()
		frame.Format = format
		if err := frame.Validate(); err != nil {
			t.Fatalf("Validate(%s) = %v", format, err)
		}
	}
}

func TestBorrowedFrameValidateRejectsInvalidCodedSize(t *testing.T) {
	for _, size := range []Size{
		{Width: 0, Height: testCodedHeight},
		{Width: testCodedWidth, Height: 0},
		{Width: -1, Height: testCodedHeight},
		{Width: testCodedWidth, Height: -1},
	} {
		frame := validFrame()
		frame.CodedSize = size
		if err := frame.Validate(); !errors.Is(err, ErrInvalidCodedSize) {
			t.Fatalf("Validate size %+v error = %v, want %v", size, err, ErrInvalidCodedSize)
		}
	}
}

func TestBorrowedFrameValidateRejectsUnsupportedFormat(t *testing.T) {
	frame := validFrame()
	frame.Format = FourCC(0x3231564e) // NV12, intentionally not supported in phase 2.
	if err := frame.Validate(); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Validate error = %v, want %v", err, ErrUnsupportedFormat)
	}
}

func TestBorrowedFrameValidateRejectsPlaneCountsOtherThanOne(t *testing.T) {
	for _, planes := range [][]Plane{
		nil,
		{},
		{{FD: testPlaneFD, Stride: testStride}, {FD: testPlaneFD + 1, Stride: testStride}},
	} {
		frame := validFrame()
		frame.Planes = planes
		if err := frame.Validate(); !errors.Is(err, ErrUnsupportedPlanes) {
			t.Fatalf("Validate plane count %d error = %v, want %v", len(planes), err, ErrUnsupportedPlanes)
		}
	}
}

func TestBorrowedFrameValidateAcceptsFDZero(t *testing.T) {
	frame := validFrame()
	frame.Planes[0].FD = 0
	if err := frame.Validate(); err != nil {
		t.Fatalf("Validate error = %v, want nil", err)
	}
}

func TestBorrowedFrameValidateRejectsInvalidPlaneFD(t *testing.T) {
	frame := validFrame()
	frame.Planes[0].FD = -1
	if err := frame.Validate(); !errors.Is(err, ErrInvalidPlaneFD) {
		t.Fatalf("Validate error = %v, want %v", err, ErrInvalidPlaneFD)
	}
}

func TestBorrowedFrameValidateRejectsZeroStride(t *testing.T) {
	frame := validFrame()
	frame.Planes[0].Stride = 0
	if err := frame.Validate(); !errors.Is(err, ErrInvalidStride) {
		t.Fatalf("Validate error = %v, want %v", err, ErrInvalidStride)
	}
}

func TestBorrowedFrameValidateRejectsStrideSmallerThanWidthTimesBPP(t *testing.T) {
	// 640x480 ARGB8888 (4 bytes/pixel) with stride=1 — regression test.
	// Before the stride-vs-width check this passed even though EGL/GDK would
	// receive an impossible pitch.
	frame := validFrame()
	frame.Planes[0].Stride = 1
	frame.Planes[0].Size = 480 // offset(0) + stride(1)*height(480) = 480 passes old extent check
	if err := frame.Validate(); !errors.Is(err, ErrInvalidStride) {
		t.Fatalf("Validate error = %v, want %v", err, ErrInvalidStride)
	}

	// stride = width*4 - 1 should also fail
	frame2 := validFrame()
	frame2.Planes[0].Stride = 2559 // 640*4 - 1
	frame2.Planes[0].Size = 2559 * 480
	if err := frame2.Validate(); !errors.Is(err, ErrInvalidStride) {
		t.Fatalf("Validate error = %v, want %v", err, ErrInvalidStride)
	}
}

func TestBorrowedFrameValidateAcceptsStrideEqualToWidthTimesBPP(t *testing.T) {
	// Boundary: stride == width*4 (2560 == 640*4)
	frame := validFrame()
	if err := frame.Validate(); err != nil {
		t.Fatalf("Validate error = %v, want nil", err)
	}
}

func TestBorrowedFrameValidateRejectsOffsetOverflow(t *testing.T) {
	// offset + stride*height overflows uint64
	frame := validFrame()
	frame.Planes[0].Stride = math.MaxUint32
	frame.Planes[0].Offset = math.MaxUint64
	if err := frame.Validate(); !errors.Is(err, ErrPlaneExtentOverflow) {
		t.Fatalf("Validate error = %v, want %v", err, ErrPlaneExtentOverflow)
	}
}

func TestBorrowedFrameValidateRejectsPlaneSizeTooSmall(t *testing.T) {
	frame := validFrame()
	// stride=2560, height=480 → rowBytes=1,228,800
	// offset 0 + 1,228,800 = 1,228,800 minimum extent
	frame.Planes[0].Size = 1_000_000
	if err := frame.Validate(); !errors.Is(err, ErrPlaneSizeTooSmall) {
		t.Fatalf("Validate error = %v, want %v", err, ErrPlaneSizeTooSmall)
	}

	// With non-zero offset
	frame2 := validFrame()
	frame2.Planes[0].Offset = 500_000
	// minExtent = 500,000 + 1,228,800 = 1,728,800
	frame2.Planes[0].Size = 1_000_000
	if err := frame2.Validate(); !errors.Is(err, ErrPlaneSizeTooSmall) {
		t.Fatalf("Validate error = %v, want %v", err, ErrPlaneSizeTooSmall)
	}
}

func TestBorrowedFrameValidateAcceptsExactPlaneSize(t *testing.T) {
	// Size exactly equal to minimum extent
	frame := validFrame()
	frame.Planes[0].Size = 2560 * 480 // stride * height
	if err := frame.Validate(); err != nil {
		t.Fatalf("Validate error = %v, want nil", err)
	}
}

func TestBorrowedFrameValidateAcceptsLargerPlaneSize(t *testing.T) {
	frame := validFrame()
	frame.Planes[0].Size = 2560 * 480 * 2
	if err := frame.Validate(); err != nil {
		t.Fatalf("Validate error = %v, want nil", err)
	}
}

func TestBorrowedFrameValidateAcceptsZeroPlaneSize(t *testing.T) {
	// Size=0 means unspecified; no size check performed.
	frame := validFrame()
	frame.Planes[0].Size = 0
	if err := frame.Validate(); err != nil {
		t.Fatalf("Validate error = %v, want nil", err)
	}
}

func TestBorrowedFrameValidateAcceptsLargeOffset(t *testing.T) {
	frame := validFrame()
	frame.Planes[0].Offset = 1 << 40
	frame.Planes[0].Size = 1<<40 + 2560*480
	if err := frame.Validate(); err != nil {
		t.Fatalf("Validate error = %v, want nil", err)
	}
}
