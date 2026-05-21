package cef2gtk

import (
	"os"
	"testing"

	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
	"github.com/bnema/puregotk/v4/gtk"
)

func TestNewViewWithOptionsRejectsInvalidBackendBeforeGTK(t *testing.T) {
	t.Setenv(backendEnvVar, "invalid")
	if got := NewViewWithOptions(ViewOptions{Backend: BackendGLArea}); got != nil {
		t.Fatal("NewViewWithOptions returned view for invalid env backend")
	}
}

func TestViewSizeScaleAndObservers(t *testing.T) {
	v := &View{}
	if w, h := v.Size(); w != 1 || h != 1 {
		t.Fatalf("initial size=(%d,%d), want (1,1)", w, h)
	}
	if got := v.DeviceScaleFactor(); got != 1 {
		t.Fatalf("initial scale=%v, want 1", got)
	}
	v.storeObservedScale(1.2)
	if got := v.DeviceScaleFactor(); got != 1.2 {
		t.Fatalf("stored fractional scale=%v, want 1.2", got)
	}

	called := false
	var gotW, gotH int32
	remove := v.AddSizeObserver(func(w, h int32) {
		called = true
		gotW, gotH = w, h
	})
	if called {
		t.Fatal("observer called for synthetic fallback size")
	}

	v.cachedWidth.Store(100)
	v.cachedHeight.Store(50)
	v.emitSizeHooks(100, 50)
	if !called || gotW != 100 || gotH != 50 {
		t.Fatalf("observer size=(%d,%d) called=%v, want (100,50) true", gotW, gotH, called)
	}
	remove()
	v.emitSizeHooks(200, 75)
	if gotW != 100 || gotH != 50 {
		t.Fatalf("observer called after remove: (%d,%d)", gotW, gotH)
	}
}

func TestAddSizeObserverImmediatelyCallsWithObservedRealSizeIncludingOneByOne(t *testing.T) {
	v := &View{}
	v.cachedWidth.Store(1)
	v.cachedHeight.Store(1)

	called := false
	remove := v.AddSizeObserver(func(w, h int32) {
		called = true
		if w != 1 || h != 1 {
			t.Fatalf("observer size=(%d,%d), want (1,1)", w, h)
		}
	})
	defer remove()

	if !called {
		t.Fatal("observer not called for real observed 1x1 size")
	}
}

func TestEffectiveInputWidgetPrefersAttachedInputWidget(t *testing.T) {
	renderWidget := &gtk.Widget{}
	inputWidget := &gtk.Widget{}
	v := &View{widget: renderWidget, input: &gtkgl.InputBridge{}, inputWidget: inputWidget}

	if got := v.effectiveInputWidget(); got != inputWidget {
		t.Fatalf("effectiveInputWidget = %p, want input widget %p", got, inputWidget)
	}
}

func TestEffectiveInputWidgetFallsBackToRenderWidgetWhenInputNotAttached(t *testing.T) {
	renderWidget := &gtk.Widget{}
	inputWidget := &gtk.Widget{}
	v := &View{widget: renderWidget, inputWidget: inputWidget}

	if got := v.effectiveInputWidget(); got != renderWidget {
		t.Fatalf("effectiveInputWidget = %p, want render widget %p", got, renderWidget)
	}
}

func TestDestroyClearsInputWidget(t *testing.T) {
	v := &View{widget: &gtk.Widget{}, inputWidget: &gtk.Widget{}}

	if err := v.Destroy(); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if v.inputWidget != nil {
		t.Fatalf("Destroy() left inputWidget set")
	}
}

func TestResolveObservedDimension(t *testing.T) {
	tests := []struct {
		name      string
		cached    int32
		allocated int32
		widget    int32
		want      int32
	}{
		{
			name:      "uses allocated size when present",
			cached:    640,
			allocated: 800,
			widget:    1,
			want:      800,
		},
		{
			name:      "preserves cached size across synthetic one pixel fallback",
			cached:    640,
			allocated: 0,
			widget:    1,
			want:      640,
		},
		{
			name:      "real allocated one pixel replaces larger cached size",
			cached:    640,
			allocated: 1,
			widget:    1,
			want:      1,
		},
		{
			name:      "allows initial one pixel bootstrap before any real size",
			cached:    0,
			allocated: 0,
			widget:    1,
			want:      1,
		},
		{
			name:      "accepts widget width above sentinel when allocation missing",
			cached:    640,
			allocated: 0,
			widget:    777,
			want:      777,
		},
		{
			name:      "keeps zero when nothing observed yet",
			cached:    0,
			allocated: 0,
			widget:    0,
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveObservedDimension(tt.cached, tt.allocated, tt.widget); got != tt.want {
				t.Fatalf("resolveObservedDimension(%d, %d, %d) = %d, want %d", tt.cached, tt.allocated, tt.widget, got, tt.want)
			}
		})
	}
}

func TestNewViewWidgetGLAreaBasics(t *testing.T) {
	if os.Getenv("PUREGO_CEF2GTK_LIVE_GTK_TEST") == "" {
		t.Skip("requires live GTK runtime; set PUREGO_CEF2GTK_LIVE_GTK_TEST=1")
	}
	t.Setenv(backendEnvVar, "glarea")
	v := NewViewWithOptions(ViewOptions{Backend: BackendGLArea})
	if v == nil {
		t.Fatal("NewView returned nil")
	}
	t.Cleanup(func() {
		if err := v.Destroy(); err != nil {
			t.Errorf("Destroy: %v", err)
		}
	})
	if got := v.Backend(); got != BackendGLArea {
		t.Fatalf("Backend = %q, want %q", got, BackendGLArea)
	}
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
