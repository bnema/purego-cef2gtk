package cef2gtk

import (
	"errors"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
)

var ErrInputNotAttached = errors.New("input bridge is not attached")

// InputOptions configures GTK-to-CEF input forwarding.
type InputOptions struct {
	// Scale is the HiDPI scale factor applied to pointer coordinates. Values <= 0 use 1.
	Scale int32
}

// AttachInput attaches GTK event controllers to the view and forwards input to host.
// Call from the GTK/main thread; GTK controller attachment is not goroutine-safe.
func (v *View) AttachInput(host cef.BrowserHost, opts InputOptions) error {
	if v == nil || v.area == nil {
		return ErrNilView
	}
	if v.input != nil {
		v.input.Detach()
	}
	v.inputScale = opts.Scale
	if v.inputScale <= 0 {
		v.inputScale = 1
	}
	v.input = gtkgl.NewInputBridge(host, v.inputScale)
	v.input.Attach(v.area)
	return nil
}

// DetachInput removes input controllers attached by AttachInput.
func (v *View) DetachInput() error {
	if v == nil {
		return ErrNilView
	}
	if v.input != nil {
		v.input.Detach()
		v.input = nil
	}
	return nil
}

// SetInputHost updates the CEF browser host used by the attached input bridge.
// Call from the GTK/main thread; fallback bridge creation/attachment is not
// goroutine-safe. If no bridge exists yet, SetInputHost creates and attaches one
// when the view still has a GtkGLArea; otherwise it returns ErrInputNotAttached.
func (v *View) SetInputHost(host cef.BrowserHost) error {
	if v == nil {
		return ErrNilView
	}
	if v.input == nil {
		if v.area == nil {
			return ErrInputNotAttached
		}
		scale := v.inputScale
		if scale <= 0 {
			scale = 1 // default if AttachInput was never called
		}
		v.input = gtkgl.NewInputBridge(host, scale)
		v.input.Attach(v.area)
		return nil
	}
	v.input.SetHost(host)
	return nil
}
