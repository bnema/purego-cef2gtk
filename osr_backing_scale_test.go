package cef2gtk

import "testing"

func TestOSRBackingScaleModeDefaultsOff(t *testing.T) {
	t.Setenv(osrBackingScaleEnvVar, "")

	if OSRBackingScaleEnabledForScale(1.25) {
		t.Fatal("backing scale enabled by default, want off without explicit mode")
	}
	if got := OSRBackingScaleFactorForScale(1.25); got != 1 {
		t.Fatalf("backing scale factor=%v, want 1", got)
	}
}

func TestOSRBackingScaleAutoEnablesOnlyAboveOne(t *testing.T) {
	t.Setenv(osrBackingScaleEnvVar, "auto")

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

func TestOSRBackingScaleForcedOn(t *testing.T) {
	t.Setenv(osrBackingScaleEnvVar, "1")

	if !OSRBackingScaleEnabledForScale(1) {
		t.Fatal("forced backing scale disabled at 1x, want enabled")
	}
	if got := OSRBackingScaleFactorForScale(1); got != 1 {
		t.Fatalf("backing scale factor=%v, want 1", got)
	}
}
