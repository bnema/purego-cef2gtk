package gtkgdk

import (
	"testing"

	"github.com/bnema/puregotk/v4/gtk"
)

// gtkRuntimeLoadable reports whether the GTK shared library can be resolved in
// this environment. Tests that reach into GTK must skip instead of panicking
// when the runtime is absent.
func gtkRuntimeLoadable() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	gtk.CheckVersion(4, 0, 0)
	return true
}

func TestGraphicsOffloadSupportedMatchesTheRuntime(t *testing.T) {
	if !gtkRuntimeLoadable() {
		t.Skip("GTK runtime unavailable")
	}
	want := gtk.CheckVersion(4, 14, 0) == ""
	if got := graphicsOffloadSupported(); got != want {
		t.Fatalf("graphicsOffloadSupported() = %v, want %v", got, want)
	}
}

func TestSelectPresenterWidgetUsesTheOffloadWrapper(t *testing.T) {
	picture := &gtk.Picture{}
	created := &gtk.GraphicsOffload{}

	offload, widget := selectPresenterWidget(picture, true, func() bool { return true },
		func(*gtk.Widget) (*gtk.GraphicsOffload, *gtk.Widget) { return created, &created.Widget })

	if offload != created {
		t.Fatalf("offload = %p, want %p", offload, created)
	}
	if widget != &created.Widget {
		t.Fatalf("widget = %p, want the offload widget %p", widget, &created.Widget)
	}
}

func TestSelectPresenterWidgetFallsBackWithoutSupport(t *testing.T) {
	picture := &gtk.Picture{}
	constructed := false

	offload, widget := selectPresenterWidget(picture, true, func() bool { return false },
		func(*gtk.Widget) (*gtk.GraphicsOffload, *gtk.Widget) {
			constructed = true
			return &gtk.GraphicsOffload{}, nil
		})

	if offload != nil {
		t.Fatalf("offload = %p, want nil when GTK lacks the widget", offload)
	}
	if widget != &picture.Widget {
		t.Fatalf("widget = %p, want the picture widget %p", widget, &picture.Widget)
	}
	if constructed {
		t.Fatal("offload constructed without runtime support")
	}
}

func TestSelectPresenterWidgetFallsBackWhenOffloadDisabled(t *testing.T) {
	picture := &gtk.Picture{}
	supported := false

	offload, widget := selectPresenterWidget(picture, false, func() bool { supported = true; return true },
		func(*gtk.Widget) (*gtk.GraphicsOffload, *gtk.Widget) { return &gtk.GraphicsOffload{}, nil })

	if offload != nil {
		t.Fatalf("offload = %p, want nil when offload is not requested", offload)
	}
	if widget != &picture.Widget {
		t.Fatalf("widget = %p, want the picture widget %p", widget, &picture.Widget)
	}
	if supported {
		t.Fatal("capability probed even though offload was not requested")
	}
}

func TestSelectPresenterWidgetFallsBackOnEmptyConstruction(t *testing.T) {
	picture := &gtk.Picture{}

	offload, widget := selectPresenterWidget(picture, true, func() bool { return true },
		func(*gtk.Widget) (*gtk.GraphicsOffload, *gtk.Widget) { return nil, nil })

	if offload != nil {
		t.Fatalf("offload = %p, want nil", offload)
	}
	if widget != &picture.Widget {
		t.Fatalf("widget = %p, want the picture widget %p", widget, &picture.Widget)
	}
}

func TestNewConfiguredOffloadSurvivesMissingSymbol(t *testing.T) {
	offload, widget := newConfiguredOffload(&gtk.Widget{}, func(*gtk.Widget) *gtk.GraphicsOffload {
		panic("core: resolve symbol gtk_graphics_offload_new")
	})
	if offload != nil || widget != nil {
		t.Fatalf("missing symbol produced (%p,%p), want (nil,nil)", offload, widget)
	}
}

func TestNewConfiguredOffloadToleratesNilChild(t *testing.T) {
	offload, widget := newConfiguredOffload(&gtk.Widget{}, func(*gtk.Widget) *gtk.GraphicsOffload {
		return nil
	})
	if offload != nil || widget != nil {
		t.Fatalf("nil constructor result produced (%p,%p), want (nil,nil)", offload, widget)
	}
}
