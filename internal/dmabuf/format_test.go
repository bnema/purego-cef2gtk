package dmabuf

import "testing"

func TestFourCCString(t *testing.T) {
	if got := FormatARGB8888.String(); got != "AR24" {
		t.Fatalf("FormatARGB8888.String() = %q, want AR24", got)
	}
	if got := FourCC(0).String(); got != "<zero>" {
		t.Fatalf("FourCC(0).String() = %q, want <zero>", got)
	}
	if got := FourCC(1).String(); got != "FourCC(0x00000001)" {
		t.Fatalf("FourCC(1).String() = %q, want FourCC(0x00000001)", got)
	}
}

func TestSupportedFormatsExactlyInitialRGBAllowlist(t *testing.T) {
	supported := []FourCC{FormatARGB8888, FormatXRGB8888, FormatABGR8888, FormatXBGR8888}
	for _, format := range supported {
		if !format.Supported() {
			t.Fatalf("%s should be supported", format)
		}
	}
	const (
		zero = FourCC(0)
		nv12 = FourCC(0x3231564e)
		ar30 = FourCC(0x30335241)
	)
	for _, format := range []FourCC{zero, nv12, ar30} {
		if format.Supported() {
			t.Fatalf("%s should not be supported", format)
		}
	}
}
