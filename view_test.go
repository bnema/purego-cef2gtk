package cef2gtk

import (
	"os"
	"testing"
)

func TestNewViewWidgetGLAreaBasics(t *testing.T) {
	if os.Getenv("PUREGO_CEF2GTK_LIVE_GTK_TEST") == "" {
		t.Skip("requires live GTK runtime; set PUREGO_CEF2GTK_LIVE_GTK_TEST=1")
	}
	v := NewView()
	if v == nil {
		t.Fatal("NewView returned nil")
	}
	t.Cleanup(func() {
		if err := v.Destroy(); err != nil {
			t.Errorf("Destroy: %v", err)
		}
	})
	if v.GLArea() == nil {
		t.Fatalf("GLArea nil")
	}
	if v.Widget() == nil {
		t.Fatalf("Widget nil")
	}
	if d := v.Diagnostics(); d.AcceleratedPaints != 0 || d.UnsupportedPaints != 0 ||
		d.AcceleratedPaintErrors != 0 || d.ImportFailures != 0 || d.RenderFailures != 0 {
		t.Fatalf("unexpected initial diagnostics: %+v", d)
	}
}
