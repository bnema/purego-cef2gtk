package cef2gtk

import (
	"math"
	"sync"
	"testing"
)

func setOSRBackingScaleEnv(t *testing.T, value string) {
	t.Helper()
	t.Setenv(osrBackingScaleEnvVar, value)
	cachedOsrBackingScaleOnce = sync.Once{}
	cachedOsrBackingScaleMode = osrBackingScaleOff
}

func TestOSRBackingScaleModeDefaultsOff(t *testing.T) {
	setOSRBackingScaleEnv(t, "")

	if OSRBackingScaleEnabledForScale(1.25) {
		t.Fatal("backing scale enabled by default, want off without explicit mode")
	}
	if got := OSRBackingScaleFactorForScale(1.25); got != 1 {
		t.Fatalf("backing scale factor=%v, want 1", got)
	}
}

func TestOSRBackingScaleAutoEnablesOnlyAboveOne(t *testing.T) {
	setOSRBackingScaleEnv(t, "auto")

	if OSRBackingScaleEnabledForScale(1) {
		t.Fatal("auto backing scale enabled at 1x, want off")
	}
	if !OSRBackingScaleEnabledForScale(1.2) {
		t.Fatal("auto backing scale disabled at 1.2x, want enabled")
	}
	if got := OSRBackingScaleFactorForScale(1.2); got != 1.2 {
		t.Fatalf("backing scale factor=%v, want 1.2", got)
	}
}

func TestOSRBackingScaleUsesEffectiveDeviceScale(t *testing.T) {
	setOSRBackingScaleEnv(t, "auto")
	v := &View{}
	v.setScaleMultiplier(1.2)
	v.storeObservedScale(1.2)

	if got := v.osrBackingScale(); math.Abs(got-1.44) > 1e-6 {
		t.Fatalf("backing scale=%v, want effective scale 1.44", got)
	}
}

func TestOSRBackingScaleForcedOn(t *testing.T) {
	setOSRBackingScaleEnv(t, "1")

	if !OSRBackingScaleEnabledForScale(1) {
		t.Fatal("forced backing scale disabled at 1x, want enabled")
	}
	if got := OSRBackingScaleFactorForScale(1); got != 1 {
		t.Fatalf("backing scale factor=%v, want 1", got)
	}
}

func TestOSRScaleContractModesAndScales(t *testing.T) {
	tests := []struct {
		name             string
		mode             string
		surfaceScale     float64
		scaleMultiplier  float64
		wantBackingScale float64
		wantScreenScale  float32
		wantCompensation float64
	}{
		{name: "off_at_one", mode: "", surfaceScale: 1, wantBackingScale: 1, wantScreenScale: 1, wantCompensation: 1},
		{name: "off_at_fractional", mode: "off", surfaceScale: 1.25, wantBackingScale: 1, wantScreenScale: 1.25, wantCompensation: 1},
		{name: "off_at_seven_quarters", mode: "off", surfaceScale: 1.75, wantBackingScale: 1, wantScreenScale: 1.75, wantCompensation: 1},
		{name: "off_at_two", mode: "off", surfaceScale: 2, wantBackingScale: 1, wantScreenScale: 2, wantCompensation: 1},
		{name: "on_at_one", mode: "1", surfaceScale: 1, wantBackingScale: 1, wantScreenScale: 1, wantCompensation: 1},
		{name: "on_at_fractional", mode: "1", surfaceScale: 1.25, wantBackingScale: 1.25, wantScreenScale: 1, wantCompensation: 1.25},
		{name: "on_at_seven_quarters", mode: "1", surfaceScale: 1.75, wantBackingScale: 1.75, wantScreenScale: 1, wantCompensation: 1.75},
		{name: "on_at_two", mode: "1", surfaceScale: 2, wantBackingScale: 2, wantScreenScale: 1, wantCompensation: 2},
		{name: "auto_threshold_at_one", mode: "auto", surfaceScale: 1, wantBackingScale: 1, wantScreenScale: 1, wantCompensation: 1},
		{name: "auto_at_fractional", mode: "auto", surfaceScale: 1.25, wantBackingScale: 1.25, wantScreenScale: 1, wantCompensation: 1.25},
		{name: "auto_at_seven_quarters", mode: "auto", surfaceScale: 1.75, wantBackingScale: 1.75, wantScreenScale: 1, wantCompensation: 1.75},
		{name: "auto_at_two", mode: "auto", surfaceScale: 2, wantBackingScale: 2, wantScreenScale: 1, wantCompensation: 2},
		{name: "auto_composes_application_scale", mode: "auto", surfaceScale: 1.25, scaleMultiplier: 1.4, wantBackingScale: 1.75, wantScreenScale: 1, wantCompensation: 1.75},
		{name: "auto_ignores_non_finite_scale", mode: "auto", surfaceScale: math.NaN(), wantBackingScale: 1, wantScreenScale: 1, wantCompensation: 1},
		{name: "auto_ignores_negative_scale", mode: "auto", surfaceScale: -2, wantBackingScale: 1, wantScreenScale: 1, wantCompensation: 1},
		{name: "on_ignores_zero_scale", mode: "1", surfaceScale: 0, wantBackingScale: 1, wantScreenScale: 1, wantCompensation: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setOSRBackingScaleEnv(t, tt.mode)
			v := &View{}
			v.storeObservedScale(tt.surfaceScale)
			v.setScaleMultiplier(tt.scaleMultiplier)

			contract := v.osrScaleContract()
			if contract.backingScale != tt.wantBackingScale {
				t.Fatalf("backing scale=%v, want %v", contract.backingScale, tt.wantBackingScale)
			}
			if contract.screenDeviceScale != tt.wantScreenScale {
				t.Fatalf("screen device scale=%v, want %v", contract.screenDeviceScale, tt.wantScreenScale)
			}
			if contract.pageZoomCompensation != tt.wantCompensation {
				t.Fatalf("page zoom compensation=%v, want %v", contract.pageZoomCompensation, tt.wantCompensation)
			}
			if got := v.PageZoomCompensation(); got != tt.wantCompensation {
				t.Fatalf("PageZoomCompensation()=%v, want %v", got, tt.wantCompensation)
			}
			if got := v.osrScreenInfoScale(); got != tt.wantScreenScale {
				t.Fatalf("osrScreenInfoScale()=%v, want %v", got, tt.wantScreenScale)
			}
		})
	}
}

func TestPageZoomCompensationDefaultsForNilView(t *testing.T) {
	setOSRBackingScaleEnv(t, "auto")
	var v *View
	if got := v.PageZoomCompensation(); got != 1 {
		t.Fatalf("nil view compensation=%v, want 1", got)
	}
	if got := (*View)(nil).osrBackingScale(); got != 1 {
		t.Fatalf("nil view backing scale=%v, want 1", got)
	}
}

func TestOSRViewRectTiesCompensationToGeometry(t *testing.T) {
	tests := []struct {
		name             string
		mode             string
		logicalWidth     int32
		logicalHeight    int32
		wantWidth        int32
		wantHeight       int32
		wantScreen       float32
		wantCompensation float64
	}{
		{name: "device_sized_ceil_non_divisible", mode: "auto", logicalWidth: 641, logicalHeight: 481, wantWidth: 802, wantHeight: 602, wantScreen: 1, wantCompensation: 1.25},
		{name: "normal_keeps_logical_size", mode: "off", logicalWidth: 641, logicalHeight: 481, wantWidth: 641, wantHeight: 481, wantScreen: 1.25, wantCompensation: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setOSRBackingScaleEnv(t, tt.mode)
			v := &View{}
			v.storeObservedScale(1.25)
			v.cachedWidth.Store(tt.logicalWidth)
			v.cachedHeight.Store(tt.logicalHeight)

			width, height := v.osrViewRectSize()
			if width != tt.wantWidth || height != tt.wantHeight {
				t.Fatalf("view rect=%dx%d, want %dx%d", width, height, tt.wantWidth, tt.wantHeight)
			}
			if got := v.PageZoomCompensation(); got != tt.wantCompensation {
				t.Fatalf("compensation=%v, want %v", got, tt.wantCompensation)
			}
			if got := v.osrScreenInfoScale(); got != tt.wantScreen {
				t.Fatalf("screen device scale=%v, want %v", got, tt.wantScreen)
			}
			// The device-sized rect must be the logical rect scaled by the page-zoom
			// compensation, rounded up: otherwise CSS pixels drift from logical pixels.
			if tt.wantCompensation > 1 {
				scaled := float64(tt.logicalWidth) * tt.wantCompensation
				if float64(width) < scaled || float64(width) >= scaled+1 {
					t.Fatalf("rect width %d is not ceil(logical %d times compensation %v=%v)", width, tt.logicalWidth, tt.wantCompensation, scaled)
				}
			}
		})
	}
}

func TestOSRScreenPointFollowsBackingContract(t *testing.T) {
	backingTests := []struct {
		name  string
		mode  string
		viewX int32
		viewY int32
		wantX int32
		wantY int32
	}{
		{name: "device_sized_passthrough", mode: "auto", viewX: 123, viewY: 456, wantX: 123, wantY: 456},
		{name: "device_sized_passthrough_negative", mode: "1", viewX: 0, viewY: -2, wantX: 0, wantY: -2},
		{name: "normal_scales_and_floors", mode: "off", viewX: 123, viewY: 456, wantX: 153, wantY: 570},
		{name: "normal_scales_negative_floor", mode: "off", viewX: 0, viewY: -2, wantX: 0, wantY: -3},
	}

	for _, tt := range backingTests {
		t.Run(tt.name, func(t *testing.T) {
			setOSRBackingScaleEnv(t, tt.mode)
			v := &View{}
			v.storeObservedScale(1.25)

			gotX, gotY := v.osrScreenPoint(tt.viewX, tt.viewY)
			if gotX != tt.wantX || gotY != tt.wantY {
				t.Fatalf("screen point=%d,%d, want %d,%d", gotX, gotY, tt.wantX, tt.wantY)
			}
		})
	}
}
