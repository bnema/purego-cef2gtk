package egl

import (
	"errors"
	"testing"
)

type fakeBackend struct {
	d       Display
	strings map[int32]string
}

func (f fakeBackend) currentDisplay() Display                  { return f.d }
func (f fakeBackend) queryString(_ Display, name int32) string { return f.strings[name] }

func TestCurrentDisplayInfoNoDisplay(t *testing.T) {
	_, err := currentDisplayInfo(fakeBackend{})
	if !errors.Is(err, ErrNoCurrentDisplay) {
		t.Fatalf("err = %v, want ErrNoCurrentDisplay", err)
	}
}

func TestCurrentDisplayInfoParsesQueriedStrings(t *testing.T) {
	info, err := currentDisplayInfo(fakeBackend{
		d: 42,
		strings: map[int32]string{
			queryVendor:     "vendor",
			queryVersion:    "1.5",
			queryClientAPIs: "OpenGL OpenGL_ES",
			queryExtensions: ExtensionDMABUFImport + " EGL_KHR_image_base",
		},
	})
	if err != nil {
		t.Fatalf("currentDisplayInfo: %v", err)
	}
	if info.Display != 42 || info.Vendor != "vendor" || info.Version != "1.5" || info.ClientAPIs != "OpenGL OpenGL_ES" {
		t.Fatalf("unexpected info: %#v", info)
	}
	if !info.SupportsDMABUFImport() {
		t.Fatal("expected DMABUF import support")
	}
}

func TestRequireExtensions(t *testing.T) {
	info := DisplayInfo{Display: 7, Extensions: ParseExtensions(ExtensionDMABUFImport)}
	if err := info.RequireExtensions(ExtensionDMABUFImport); err != nil {
		t.Fatalf("RequireExtensions: %v", err)
	}
	if err := info.RequireExtensions("EGL_missing"); !errors.Is(err, ErrMissingDisplaySupport) {
		t.Fatalf("err = %v, want ErrMissingDisplaySupport", err)
	}
	if err := (DisplayInfo{}).RequireExtensions(ExtensionDMABUFImport); !errors.Is(err, ErrNoCurrentDisplay) {
		t.Fatalf("err = %v, want ErrNoCurrentDisplay", err)
	}
}
