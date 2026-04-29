package dmabuf

import (
	"errors"
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

func TestBorrowedFrameValidateRejectsInvalidPlaneFD(t *testing.T) {
	frame := validFrame()
	frame.Planes[0].FD = 0
	if err := frame.Validate(); !errors.Is(err, ErrInvalidPlaneFD) {
		t.Fatalf("Validate error = %v, want %v", err, ErrInvalidPlaneFD)
	}
	// Also reject negative FD.
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
