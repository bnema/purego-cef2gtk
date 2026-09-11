package cef2gtk

import (
	"math"
	"os"
	"strings"
	"sync"
)

const osrBackingScaleEnvVar = "PUREGO_CEF2GTK_OSR_BACKING_SCALE"

var (
	cachedOsrBackingScaleMode osrBackingScaleMode
	cachedOsrBackingScaleOnce sync.Once
)

type osrBackingScaleMode uint8

const (
	osrBackingScaleOff osrBackingScaleMode = iota
	osrBackingScaleOn
	osrBackingScaleAuto
)

func osrBackingScaleModeFromEnv() osrBackingScaleMode {
	cachedOsrBackingScaleOnce.Do(func() {
		cachedOsrBackingScaleMode = parseOSRBackingScaleMode(os.Getenv(osrBackingScaleEnvVar))
	})
	return cachedOsrBackingScaleMode
}

func parseOSRBackingScaleMode(value string) osrBackingScaleMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "device":
		return osrBackingScaleOn
	case "auto":
		return osrBackingScaleAuto
	default:
		return osrBackingScaleOff
	}
}

// osrScaleContract is the one derivation of the OSR geometry, rendered screen
// scale, and page-zoom compensation implied by an effective GTK scale.
//
// Current CEF shared-texture OSR builds can accept a fractional
// CefScreenInfo.device_scale_factor while still emitting 1x/logical DMABUF
// frames, so purego-cef2gtk serves two contracts:
//
//   - Normal: the view rect spans the GTK logical widget size and CEF receives
//     the effective scale as device_scale_factor. One CSS pixel is one logical
//     pixel, so page zoom needs no compensation
//     (backingScale 1, screenDeviceScale effective, compensation 1).
//   - Device-sized backing: the view rect is scaled to device pixels while CEF
//     receives device_scale_factor 1. The CSS viewport then spans backingScale
//     times the logical widget size, so an application that exposes page zoom
//     must multiply its user-facing page zoom by compensation, and divide CEF
//     zoom readback by the same factor, to keep one CSS pixel equal to one
//     logical pixel (backingScale = effective scale, screenDeviceScale 1,
//     compensation = effective scale).
//
// Compensation is applied to page zoom by the embedding application. It is not
// user zoom: user zoom stays a user-facing multiplier that survives output and
// density changes unchanged.
type osrScaleContract struct {
	backingScale         float64
	screenDeviceScale    float32
	pageZoomCompensation float64
}

func osrScaleContractForScale(scale float64) osrScaleContract {
	scale = normalizeDeviceScale(scale)
	if !osrBackingScaleEnabledForScale(scale) {
		return osrScaleContract{backingScale: 1, screenDeviceScale: float32(scale), pageZoomCompensation: 1}
	}
	return osrScaleContract{backingScale: scale, screenDeviceScale: 1, pageZoomCompensation: scale}
}

func (v *View) osrScaleContract() osrScaleContract {
	if v == nil {
		return osrScaleContract{backingScale: 1, screenDeviceScale: 1, pageZoomCompensation: 1}
	}
	return osrScaleContractForScale(float64(v.DeviceScaleFactor()))
}

// OSRBackingScaleEnabledForScale reports whether the Linux accelerated OSR
// HiDPI compatibility path is active for the provided GTK surface scale.
func OSRBackingScaleEnabledForScale(scale float64) bool {
	return osrBackingScaleEnabledForScale(scale)
}

func osrBackingScaleEnabledForScale(scale float64) bool {
	scale = normalizeDeviceScale(scale)
	switch osrBackingScaleModeFromEnv() {
	case osrBackingScaleOn:
		return true
	case osrBackingScaleAuto:
		return scale > 1
	default:
		return false
	}
}

// OSRBackingScaleFactorForScale returns the backing scale that should be used
// for CEF OSR view/input coordinates. It returns 1 when compatibility scaling
// is disabled for the provided surface scale.
func OSRBackingScaleFactorForScale(scale float64) float64 {
	return osrScaleContractForScale(scale).backingScale
}

// PageZoomCompensation returns the factor an application that exposes page zoom
// must multiply its user-facing zoom by before calling CEF SetZoomLevel, so the
// page's CSS viewport keeps matching the GTK logical view size under the active
// OSR contract. CEF zoom readback must be divided by the same factor.
//
// It returns 1 for a nil view and whenever CEF's normal logical OSR contract is
// in effect. The value follows the observed effective scale, so it changes when
// the view moves to an output with a different scale.
func (v *View) PageZoomCompensation() float64 {
	return v.osrScaleContract().pageZoomCompensation
}

func (v *View) osrBackingScaleEnabled() bool {
	if v == nil {
		return false
	}
	return osrBackingScaleEnabledForScale(float64(v.DeviceScaleFactor()))
}

func (v *View) osrBackingScale() float64 {
	return v.osrScaleContract().backingScale
}

func (v *View) osrViewRectSize() (int32, int32) {
	width, height := v.cachedSize()
	scale := v.osrBackingScale()
	return scaleDimension(width, scale), scaleDimension(height, scale)
}

func (v *View) osrScreenInfoScale() float32 {
	return v.osrScaleContract().screenDeviceScale
}

func (v *View) osrScreenPoint(viewX, viewY int32) (int32, int32) {
	if v.osrBackingScaleEnabled() {
		// With device-sized OSR backing CEF view coordinates are already device
		// pixels because GetScreenInfo reports a 1x scale.
		return viewX, viewY
	}
	scale := normalizeDeviceScale(float64(v.DeviceScaleFactor()))
	return scaleCoordinate(viewX, scale), scaleCoordinate(viewY, scale)
}

func inputScaleForOSRBacking(fallback float64) float64 {
	return osrScaleContractForScale(fallback).backingScale
}

func scaleDimension(value int32, scale float64) int32 {
	if value <= 0 {
		return 1
	}
	scale = normalizeDeviceScale(scale)
	if scale <= 1 {
		return value
	}
	return int32(math.Ceil(float64(value) * scale))
}

func scaleCoordinate(value int32, scale float64) int32 {
	return int32(math.Floor(float64(value) * normalizeDeviceScale(scale)))
}
