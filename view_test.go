package cef2gtk

import (
	"os"
	"testing"
)

func TestViewSizeScaleAndObservers(t *testing.T) {
	v := &View{}
	if w, h := v.Size(); w != 1 || h != 1 {
		t.Fatalf("initial size=(%d,%d), want (1,1)", w, h)
	}
	if got := v.DeviceScaleFactor(); got != 1 {
		t.Fatalf("initial scale=%v, want 1", got)
	}

	var gotW, gotH int32
	remove := v.AddSizeObserver(func(w, h int32) { gotW, gotH = w, h })
	v.cachedWidth.Store(100)
	v.cachedHeight.Store(50)
	v.emitSizeHooks(100, 50)
	if gotW != 100 || gotH != 50 {
		t.Fatalf("observer size=(%d,%d), want (100,50)", gotW, gotH)
	}
	remove()
	v.emitSizeHooks(200, 75)
	if gotW != 100 || gotH != 50 {
		t.Fatalf("observer called after remove: (%d,%d)", gotW, gotH)
	}
}

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
