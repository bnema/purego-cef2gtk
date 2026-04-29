package egl

import "testing"

func TestParseExtensions(t *testing.T) {
	exts := ParseExtensions(" EGL_EXT_image_dma_buf_import  EGL_KHR_image_base\nEGL_EXT_image_dma_buf_import ")
	if !exts.Has(ExtensionDMABUFImport) {
		t.Fatalf("expected %s", ExtensionDMABUFImport)
	}
	if !exts.Has("EGL_KHR_image_base") {
		t.Fatal("expected EGL_KHR_image_base")
	}
	if exts.Has("") || exts.Has("EGL_missing") {
		t.Fatal("unexpected extension present")
	}
	if got := len(exts); got != 2 {
		t.Fatalf("len = %d, want 2", got)
	}
}

func TestExtensionsMissing(t *testing.T) {
	exts := ParseExtensions("EGL_one EGL_two")
	missing := exts.Missing("EGL_one", "EGL_three")
	if len(missing) != 1 || missing[0] != "EGL_three" {
		t.Fatalf("missing = %#v", missing)
	}
}
