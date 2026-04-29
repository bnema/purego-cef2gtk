package cef2gtk

import (
	"errors"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
)

var ErrInputNotAttached = errors.New("input bridge is not attached")
var ErrViewNotInitialized = errors.New("view not initialized")

// InputOptions configures GTK-to-CEF input forwarding.
type InputOptions struct {
	// Scale is the HiDPI scale factor applied to pointer coordinates. Values <= 0 use 1.
	Scale int32
}

func (o InputOptions) normalizedScale() int32 {
	if o.Scale <= 0 {
		return 1
	}
	return o.Scale
}

// AttachInput attaches GTK event controllers to the view and forwards input to host.
// Call from the GTK/main thread; GTK controller attachment is not goroutine-safe.
func (v *View) AttachInput(host cef.BrowserHost, opts InputOptions) error {
	if v == nil {
		return ErrNilView
	}
	if v.area == nil {
		return ErrViewNotInitialized
	}
	if v.input != nil {
		v.input.Detach()
	}
	v.inputScale = opts.normalizedScale()
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
// Must be called on the GTK/main thread. Returns ErrInputNotAttached when v.input
// is nil. Callers must call AttachInput first.
func (v *View) SetInputHost(host cef.BrowserHost) error {
	if v == nil {
		return ErrNilView
	}
	if v.input == nil {
		return ErrInputNotAttached
	}
	v.input.SetHost(host)
	return nil
}
