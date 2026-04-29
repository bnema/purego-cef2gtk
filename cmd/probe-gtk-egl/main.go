package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gtk"
)

type output struct {
	Status string                    `json:"status"`
	Error  string                    `json:"error,omitempty"`
	Probe  *gtkgl.ContextProbeResult `json:"probe,omitempty"`
}

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if os.Getenv("WAYLAND_DISPLAY") == "" {
		write(output{Status: "skipped", Error: "WAYLAND_DISPLAY is not set; Wayland GTK/EGL probe not run"})
		os.Exit(2)
	}
	if !gtk.InitCheck() {
		write(output{Status: "skipped", Error: "gtk.InitCheck failed; no usable GTK display"})
		os.Exit(2)
	}

	area := gtk.NewGLArea()
	if area == nil {
		write(output{Status: "error", Error: "gtk.NewGLArea returned nil"})
		os.Exit(1)
	}
	area.SetAllowedApis(gdk.GlApiGlValue | gdk.GlApiGlesValue)
	area.SetSizeRequest(16, 16)

	win := gtk.NewWindow()
	if win == nil {
		write(output{Status: "error", Error: "gtk.NewWindow returned nil"})
		os.Exit(1)
	}
	win.SetChild(&area.Widget)
	cleanup := func() { win.Destroy() }
	win.Realize()
	area.Realize()

	probe, err := gtkgl.ProbeCurrentGLAreaContext(area)
	if err != nil {
		status := "error"
		code := 1
		if errors.Is(err, gtkgl.ErrNonWaylandBackend) {
			status = "unsupported"
			code = 2
		}
		write(output{Status: status, Error: err.Error(), Probe: &probe})
		cleanup()
		os.Exit(code)
	}
	write(output{Status: "ok", Probe: &probe})
	cleanup()
}

func write(v output) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal diagnostics: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}
