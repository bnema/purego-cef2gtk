package gtkgdk

import (
	"testing"

	"github.com/bnema/puregotk/v4/gtk"
)

// runtimeLoadable reports whether a native library can be resolved in this
// environment. Tests that reach into GTK or GLib must skip instead of panicking
// when the runtime is absent.
func runtimeLoadable(probe func()) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	probe()
	return true
}

func gtkRuntimeLoadable() bool {
	return runtimeLoadable(func() { gtk.GetMajorVersion() })
}

func TestGraphicsOffloadSupportFollowsTheGtkVersion(t *testing.T) {
	cases := []struct {
		major, minor uint
		want         bool
	}{
		{major: 3, minor: 24, want: false},
		{major: 4, minor: 0, want: false},
		{major: 4, minor: 13, want: false},
		{major: 4, minor: 14, want: true},
		{major: 4, minor: 22, want: true},
		{major: 5, minor: 0, want: true},
	}
	for _, testCase := range cases {
		if got := graphicsOffloadSupportedBy(testCase.major, testCase.minor); got != testCase.want {
			t.Fatalf("graphicsOffloadSupportedBy(%d,%d) = %v, want %v",
				testCase.major, testCase.minor, got, testCase.want)
		}
	}
}

func TestGraphicsOffloadSupportedMatchesTheRuntime(t *testing.T) {
	if !gtkRuntimeLoadable() {
		t.Skip("GTK runtime unavailable")
	}
	// The predicate covers the version arithmetic; this pins the runtime
	// versions the presenter actually reads.
	want := graphicsOffloadSupportedBy(gtk.GetMajorVersion(), gtk.GetMinorVersion())
	if got := graphicsOffloadSupported(); got != want {
		t.Fatalf("graphicsOffloadSupported() = %v, want %v", got, want)
	}
}

func TestConfigureOffloadPresenterSetsTheWrapperContract(t *testing.T) {
	presenter := &fakeOffloadPresenter{}

	configureOffloadPresenter(presenter)

	if presenter.enabled == nil || *presenter.enabled != gtk.GraphicsOffloadEnabledValue {
		t.Fatalf("enabled = %v, want %v", presenter.enabled, gtk.GraphicsOffloadEnabledValue)
	}
	if presenter.hexpand == nil || !*presenter.hexpand {
		t.Fatalf("hexpand = %v, want true", presenter.hexpand)
	}
	if presenter.vexpand == nil || !*presenter.vexpand {
		t.Fatalf("vexpand = %v, want true", presenter.vexpand)
	}
	if presenter.width != 1 || presenter.height != 1 {
		t.Fatalf("size request = %dx%d, want 1x1", presenter.width, presenter.height)
	}
	if presenter.requests != 4 {
		t.Fatalf("configuration calls = %d, want 4", presenter.requests)
	}
}

func TestConfigureOffloadPresenterToleratesNil(t *testing.T) {
	configureOffloadPresenter(nil)
}

// fakeOffloadPresenter records the calls the presenter makes on the wrapper.
type fakeOffloadPresenter struct {
	enabled  *gtk.GraphicsOffloadEnabled
	hexpand  *bool
	vexpand  *bool
	width    int
	height   int
	requests int
}

func (f *fakeOffloadPresenter) SetEnabled(enabled gtk.GraphicsOffloadEnabled) {
	f.enabled = &enabled
	f.requests++
}

func (f *fakeOffloadPresenter) SetHexpand(expand bool) {
	f.hexpand = &expand
	f.requests++
}

func (f *fakeOffloadPresenter) SetVexpand(expand bool) {
	f.vexpand = &expand
	f.requests++
}

func (f *fakeOffloadPresenter) SetSizeRequest(width, height int) {
	f.width, f.height = width, height
	f.requests++
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

func TestConstructOffloadSurvivesMissingSymbol(t *testing.T) {
	offload := constructOffload(func(*gtk.Widget) *gtk.GraphicsOffload {
		panic("core: resolve symbol gtk_graphics_offload_new")
	}, &gtk.Widget{})
	if offload != nil {
		t.Fatalf("missing symbol produced %p, want nil", offload)
	}
}

func TestConstructOffloadToleratesNilConstruction(t *testing.T) {
	offload := constructOffload(func(*gtk.Widget) *gtk.GraphicsOffload {
		return nil
	}, &gtk.Widget{})
	if offload != nil {
		t.Fatalf("nil constructor result produced %p, want nil", offload)
	}
}
