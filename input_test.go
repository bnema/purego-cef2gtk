package cef2gtk

import (
	"math"
	"testing"
)

func TestInputOptionsNormalizedScale(t *testing.T) {
	setOSRBackingScaleEnv(t, "")
	if got := (InputOptions{}).normalizedScale(1.25); got != 1 {
		t.Fatalf("zero scale normalized to %v, want 1", got)
	}
	if got := (InputOptions{Scale: -2}).normalizedScale(1.25); got != 1 {
		t.Fatalf("negative scale normalized to %v, want 1", got)
	}
	if got := (InputOptions{Scale: 2}).normalizedScale(1.25); got != 2 {
		t.Fatalf("scale normalized to %v, want 2", got)
	}
}

func TestInputOptionsNormalizedScaleUsesDeviceScaleForBackingScale(t *testing.T) {
	setOSRBackingScaleEnv(t, "1")
	if got := (InputOptions{}).normalizedScale(1.25); got != 1.25 {
		t.Fatalf("zero scale normalized to %v, want 1.25 when backing scale enabled", got)
	}
}

func TestInputOptionsNormalizedScaleUsesDeviceScaleForAutoBackingScale(t *testing.T) {
	setOSRBackingScaleEnv(t, "auto")
	if got := (InputOptions{}).normalizedScale(1.25); got != 1.25 {
		t.Fatalf("zero scale normalized to %v, want 1.25 when auto backing scale enabled", got)
	}
	if got := (InputOptions{}).normalizedScale(1); got != 1 {
		t.Fatalf("1x scale normalized to %v, want 1", got)
	}
}

func TestInputScaleOverrideRemainsStickyAcrossObservedScaleChanges(t *testing.T) {
	v := &View{}
	v.setInputScaleOverride(2)

	if got := v.inputScaleForObservedScale(1.25); got != 2 {
		t.Fatalf("input scale with explicit override = %v, want 2", got)
	}
	if got := v.inputScaleForObservedScale(1.75); got != 2 {
		t.Fatalf("input scale after observed scale change = %v, want sticky override 2", got)
	}

	v.setInputScaleOverride(math.NaN())
	setOSRBackingScaleEnv(t, "auto")
	if got := v.inputScaleForObservedScale(1.25); got != 1.25 {
		t.Fatalf("input scale after clearing override = %v, want auto 1.25", got)
	}

	v.setInputScaleOverride(math.Inf(1))
	if got := v.inputScaleForObservedScale(1.25); got != 1.25 {
		t.Fatalf("input scale after infinite override = %v, want auto 1.25", got)
	}
}
