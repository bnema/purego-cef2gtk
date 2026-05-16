package cef2gtk

import (
	"errors"
	"math"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
)

var ErrInputNotAttached = errors.New("input bridge is not attached")
var ErrViewNotInitialized = errors.New("view not initialized")

// InputOptions configures GTK-to-CEF input forwarding.
type InputOptions struct {
	// Scale overrides the coordinate transform applied to pointer coordinates.
	// Values <= 0 let the view choose the correct transform for the current OSR
	// backing mode: logical coordinates normally, device coordinates when the
	// OSR backing-scale compatibility path is active.
	Scale float64
	// OnMiddleClick is invoked when GTK receives a middle-button press. Returning
	// true consumes the event before it is forwarded to CEF.
	OnMiddleClick func(x, y float64) bool
	// SelectionText returns the current browser selection for explicit clipboard
	// shortcuts. When set with OnClipboardShortcut, Ctrl+C/Ctrl+X can be mirrored
	// to application-level clipboard orchestration before forwarding to CEF.
	SelectionText func() string
	// OnClipboardShortcut is invoked for explicit Ctrl+C/Ctrl+X shortcuts when
	// SelectionText returns non-empty text. action is "copy" or "cut".
	OnClipboardShortcut func(action, text string)
}

func (o InputOptions) normalizedScale(fallback float64) float64 {
	if o.Scale > 0 {
		return normalizeDeviceScale(o.Scale)
	}
	if fallback <= 0 {
		return 1
	}
	return inputScaleForOSRBacking(fallback)
}

func (v *View) setInputScaleOverride(scale float64) {
	if v == nil {
		return
	}
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		v.inputScaleOverride.Store(0)
		return
	}
	v.inputScaleOverride.Store(math.Float64bits(normalizeDeviceScale(scale)))
}

func (v *View) inputScaleForObservedScale(observedScale float64) float64 {
	if v == nil {
		return 1
	}
	if bits := v.inputScaleOverride.Load(); bits != 0 {
		return normalizeDeviceScale(math.Float64frombits(bits))
	}
	return inputScaleForOSRBacking(observedScale)
}

// AttachInput attaches GTK event controllers to the view and forwards input to host.
// Call from the GTK/main thread; GTK controller attachment is not goroutine-safe.
func (v *View) AttachInput(host cef.BrowserHost, opts InputOptions) error {
	if v == nil {
		return ErrNilView
	}
	if v.widget == nil {
		return ErrViewNotInitialized
	}
	if v.input != nil {
		v.input.Detach()
	}
	v.setInputScaleOverride(opts.Scale)
	scale := v.inputScaleForObservedScale(float64(v.DeviceScaleFactor()))
	v.input = gtkgl.NewInputBridge(host, scale)
	v.input.SetMiddleClickHandler(opts.OnMiddleClick)
	v.input.SetClipboardShortcutHandler(opts.SelectionText, opts.OnClipboardShortcut)
	v.input.AttachToWidget(v.widget)
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
	v.setInputScaleOverride(0)
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
