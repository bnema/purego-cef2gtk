package egl

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
)

const (
	wantCodedWidth  int32  = 321
	wantCodedHeight int32  = 654
	wantFD          int    = 17
	wantStride      uint32 = 4096
	wantOffset      uint64 = 128
	wantPlaneSize   uint64 = 262144
	wantModifier    uint64 = 0x0102030405060708
)

func TestDMABUFImageAttributesWithoutModifier(t *testing.T) {
	frame := validFrame()
	frame.Modifier = dmabuf.ModifierInvalid

	attrs, err := DMABUFImageAttributes(frame, Extensions{ExtensionDMABUFImport: {}})
	if err != nil {
		t.Fatalf("DMABUFImageAttributes returned error: %v", err)
	}

	want := []Attribute{
		attributeWidth, Attribute(wantCodedWidth),
		attributeHeight, Attribute(wantCodedHeight),
		attributeLinuxDRMFourCC, Attribute(dmabuf.FormatARGB8888),
		attributeDMABUFPlane0FD, Attribute(wantFD),
		attributeDMABUFPlane0Offset, Attribute(wantOffset),
		attributeDMABUFPlane0Pitch, Attribute(wantStride),
		attributeNone,
	}
	if !reflect.DeepEqual(attrs, want) {
		t.Fatalf("attrs = %#v, want %#v", attrs, want)
	}
}

func TestDMABUFImageAttributesWithLinearModifier(t *testing.T) {
	frame := validFrame()
	frame.Modifier = dmabuf.ModifierLinear

	attrs, err := DMABUFImageAttributes(frame, Extensions{
		ExtensionDMABUFImport:          {},
		ExtensionDMABUFImportModifiers: {},
	})
	if err != nil {
		t.Fatalf("DMABUFImageAttributes returned error: %v", err)
	}

	wantSuffix := []Attribute{
		attributeDMABUFPlane0ModLoEXT, 0,
		attributeDMABUFPlane0ModHiEXT, 0,
		attributeNone,
	}
	if got := attrs[len(attrs)-len(wantSuffix):]; !reflect.DeepEqual(got, wantSuffix) {
		t.Fatalf("linear modifier attrs = %#v, want %#v", got, wantSuffix)
	}
}

func TestDMABUFImageAttributesWithModifier(t *testing.T) {
	frame := validFrame()
	frame.Modifier = wantModifier

	attrs, err := DMABUFImageAttributes(frame, Extensions{
		ExtensionDMABUFImport:          {},
		ExtensionDMABUFImportModifiers: {},
	})
	if err != nil {
		t.Fatalf("DMABUFImageAttributes returned error: %v", err)
	}

	hi, lo := dmabuf.ModifierHiLo(wantModifier)
	wantSuffix := []Attribute{
		attributeDMABUFPlane0ModLoEXT, Attribute(lo),
		attributeDMABUFPlane0ModHiEXT, Attribute(hi),
		attributeNone,
	}
	if got := attrs[len(attrs)-len(wantSuffix):]; !reflect.DeepEqual(got, wantSuffix) {
		t.Fatalf("modifier attrs = %#v, want %#v", got, wantSuffix)
	}
}

func TestDMABUFImageAttributesRejectsMissingBaseExtension(t *testing.T) {
	_, err := DMABUFImageAttributes(validFrame(), nil)
	if !errors.Is(err, ErrMissingDisplaySupport) {
		t.Fatalf("error = %v, want ErrMissingDisplaySupport", err)
	}
}

func TestDMABUFImageAttributesRejectsModifierWithoutExtension(t *testing.T) {
	frame := validFrame()
	frame.Modifier = wantModifier

	_, err := DMABUFImageAttributes(frame, Extensions{ExtensionDMABUFImport: {}})
	if !errors.Is(err, ErrMissingDisplaySupport) {
		t.Fatalf("error = %v, want ErrMissingDisplaySupport", err)
	}
}

func TestDMABUFImageAttributesRejectsInvalidFrame(t *testing.T) {
	frame := validFrame()
	frame.Planes = append(frame.Planes, dmabuf.Plane{FD: 18, Stride: 1})

	_, err := DMABUFImageAttributes(frame, Extensions{ExtensionDMABUFImport: {}})
	if !errors.Is(err, dmabuf.ErrUnsupportedPlanes) {
		t.Fatalf("error = %v, want ErrUnsupportedPlanes", err)
	}
}

func TestDMABUFImageAttributesRejectsUnsupportedFormat(t *testing.T) {
	frame := validFrame()
	frame.Format = dmabuf.FourCC(0xdeadbeef)

	_, err := DMABUFImageAttributes(frame, Extensions{ExtensionDMABUFImport: {}})
	if !errors.Is(err, dmabuf.ErrUnsupportedFormat) {
		t.Fatalf("error = %v, want ErrUnsupportedFormat", err)
	}
}

func TestDMABUFImageAttributesRejectsOffsetTooLargeForEGLInt(t *testing.T) {
	frame := validFrame()
	frame.Planes[0].Offset = uint64(math.MaxInt32) + 1

	_, err := DMABUFImageAttributes(frame, Extensions{ExtensionDMABUFImport: {}})
	if !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("error = %v, want ErrInvalidOffset", err)
	}
}

func TestDMABUFImageAttributesRejectsStrideTooLargeForEGLInt(t *testing.T) {
	frame := validFrame()
	frame.Planes[0].Stride = uint32(math.MaxInt32) + 1

	_, err := DMABUFImageAttributes(frame, Extensions{ExtensionDMABUFImport: {}})
	if !errors.Is(err, ErrInvalidStrideAttribute) {
		t.Fatalf("error = %v, want ErrInvalidStrideAttribute", err)
	}
}

func validFrame() dmabuf.BorrowedFrame {
	return dmabuf.BorrowedFrame{
		CodedSize: dmabuf.Size{Width: wantCodedWidth, Height: wantCodedHeight},
		Format:    dmabuf.FormatARGB8888,
		Modifier:  dmabuf.ModifierInvalid,
		Planes: []dmabuf.Plane{{
			FD:     wantFD,
			Stride: wantStride,
			Offset: wantOffset,
			Size:   wantPlaneSize,
		}},
	}
}
