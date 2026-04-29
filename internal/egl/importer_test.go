package egl

import (
	"errors"
	"strings"
	"testing"
	"unsafe"

	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
)

func validImportFrame() dmabuf.BorrowedFrame {
	return dmabuf.BorrowedFrame{
		CodedSize: dmabuf.Size{Width: 64, Height: 32},
		Format:    dmabuf.FormatARGB8888,
		Modifier:  dmabuf.ModifierInvalid,
		Planes:    []dmabuf.Plane{{FD: 7, Stride: 256}},
	}
}

func TestImporterImportDMABUFCreatesImageWithAttrs(t *testing.T) {
	var gotDisplay uintptr
	var gotTarget uint32
	var gotAttrs []Attribute
	imp := &Importer{
		display:    42,
		extensions: ParseExtensions(ExtensionDMABUFImport),
		create: func(display uintptr, context uintptr, target uint32, buffer uintptr, attrs *Attribute) uintptr {
			gotDisplay = display
			gotTarget = target
			for p := attrs; *p != attributeNone; p = (*Attribute)(unsafe.Add(unsafe.Pointer(p), unsafe.Sizeof(Attribute(0)))) {
				gotAttrs = append(gotAttrs, *p)
			}
			gotAttrs = append(gotAttrs, attributeNone)
			return 99
		},
		getError: func() uint32 { return success },
	}
	image, err := imp.ImportDMABUF(validImportFrame())
	if err != nil {
		t.Fatalf("ImportDMABUF: %v", err)
	}
	if image != 99 || gotDisplay != 42 || gotTarget != targetLinuxDMABUFExt {
		t.Fatalf("unexpected import: image=%v display=%v target=0x%x", image, gotDisplay, gotTarget)
	}
	if gotAttrs[len(gotAttrs)-1] != attributeNone {
		t.Fatalf("attrs not terminated: %v", gotAttrs)
	}
}

func TestImporterImportDMABUFReportsEGLError(t *testing.T) {
	imp := &Importer{
		display:    42,
		extensions: ParseExtensions(ExtensionDMABUFImport),
		create:     func(uintptr, uintptr, uint32, uintptr, *Attribute) uintptr { return 0 },
		getError:   func() uint32 { return badAttribute },
	}
	_, err := imp.ImportDMABUF(validImportFrame())
	if err == nil || !strings.Contains(err.Error(), "EGL_BAD_ATTRIBUTE") {
		t.Fatalf("expected EGL error, got %v", err)
	}
}

func TestImporterDestroyReportsEGLError(t *testing.T) {
	imp := &Importer{
		display:  42,
		destroy:  func(uintptr, uintptr) uint32 { return 0 },
		getError: func() uint32 { return badDisplay },
	}
	err := imp.Destroy(5)
	if err == nil || !strings.Contains(err.Error(), "EGL_BAD_DISPLAY") {
		t.Fatalf("expected EGL destroy error, got %v", err)
	}
}

func TestImporterRequiresDMABUFExtension(t *testing.T) {
	imp := &Importer{display: 42, extensions: ParseExtensions(""), create: func(uintptr, uintptr, uint32, uintptr, *Attribute) uintptr { return 1 }}
	_, err := imp.ImportDMABUF(validImportFrame())
	if !errors.Is(err, ErrMissingDisplaySupport) {
		t.Fatalf("err = %v, want ErrMissingDisplaySupport", err)
	}
}
