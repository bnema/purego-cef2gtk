package cef2gtk

import "testing"

func TestOptionsValidateAcceptsDefaultRenderingMode(t *testing.T) {
	if err := (Options{}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestOptionsValidateAcceptsAcceleratedDMABUF(t *testing.T) {
	opts := Options{RenderingMode: RenderingModeAcceleratedDMABUF}
	if err := opts.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestOptionsValidateRejectsUnsupportedRenderingMode(t *testing.T) {
	opts := Options{RenderingMode: RenderingMode("software")}
	if err := opts.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestOptionsNormalizedAppliesDefaultRenderingMode(t *testing.T) {
	got, err := (Options{}).normalized()
	if err != nil {
		t.Fatalf("normalized() error = %v", err)
	}
	if got.RenderingMode != RenderingModeAcceleratedDMABUF {
		t.Fatalf("RenderingMode = %q, want %q", got.RenderingMode, RenderingModeAcceleratedDMABUF)
	}
}
