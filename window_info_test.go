package cef2gtk

import (
	"testing"

	"github.com/bnema/purego-cef/cef"
)

func TestConfigureWindowInfoEnablesWindowlessSharedTexture(t *testing.T) {
	info := cef.NewWindowInfo()

	ConfigureWindowInfo(&info, WindowInfoOptions{Parent: 42})

	if info.WindowlessRenderingEnabled != 1 {
		t.Fatalf("WindowlessRenderingEnabled = %d, want 1", info.WindowlessRenderingEnabled)
	}
	if info.SharedTextureEnabled != 1 {
		t.Fatalf("SharedTextureEnabled = %d, want 1", info.SharedTextureEnabled)
	}
	if info.ParentWindow != cef.WindowHandle(42) {
		t.Fatalf("ParentWindow = %v, want 42", info.ParentWindow)
	}
}

func TestConfigureWindowInfoNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ConfigureWindowInfo panicked with nil info: %v", r)
		}
	}()

	ConfigureWindowInfo(nil, WindowInfoOptions{Parent: 42})
}
