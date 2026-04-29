package cef2gtk

import "fmt"

// RenderingMode selects the rendering pipeline used by the GTK bridge.
type RenderingMode string

const (
	// RenderingModeAcceleratedDMABUF uses CEF accelerated OSR shared textures
	// imported through Wayland DMABUF/EGL and rendered by GtkGLArea.
	RenderingModeAcceleratedDMABUF RenderingMode = "accelerated-dmabuf"
)

// Options configures a View.
type Options struct {
	// RenderingMode must be RenderingModeAcceleratedDMABUF. It defaults to that
	// mode when left empty.
	RenderingMode RenderingMode
}

// Validate verifies that the options request the supported accelerated path.
func (o Options) Validate() error {
	mode := o.RenderingMode
	if mode == "" {
		mode = RenderingModeAcceleratedDMABUF
	}
	if mode != RenderingModeAcceleratedDMABUF {
		return fmt.Errorf("unsupported rendering mode %q", o.RenderingMode)
	}
	return nil
}

// normalized returns options with defaults applied after validation.
func (o Options) normalized() (Options, error) {
	if o.RenderingMode == "" {
		o.RenderingMode = RenderingModeAcceleratedDMABUF
	}
	if err := o.Validate(); err != nil {
		return Options{}, err
	}
	return o, nil
}
