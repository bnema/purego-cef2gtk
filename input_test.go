package cef2gtk

import (
	"math"
	"testing"

	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
	"github.com/bnema/puregotk/v4/gdk"
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

func TestScrollOptionsConvertsToGTKGL(t *testing.T) {
	got := toGTKGLScrollOptions(ScrollOptions{
		WheelMultiplier:      1.5,
		PreciseMultiplier:    2.5,
		HorizontalMultiplier: 0.75,
		VerticalMultiplier:   1.25,
		MaxDelta:             120,
	})

	if got.WheelMultiplier != 1.5 ||
		got.PreciseMultiplier != 2.5 ||
		got.HorizontalMultiplier != 0.75 ||
		got.VerticalMultiplier != 1.25 ||
		got.MaxDelta != 120 {
		t.Fatalf("converted scroll options = %+v", got)
	}
}

func TestNavigationSwipeOptionsConvertsToGTKGL(t *testing.T) {
	got := toGTKGLNavigationSwipeOptions(NavigationSwipeOptions{
		Enabled:          true,
		MinDelta:         15,
		MaxVerticalRatio: 0.5,
	})

	if !got.Enabled || got.MinDelta != 15 || got.MaxVerticalRatio != 0.5 {
		t.Fatalf("converted navigation swipe options = %+v", got)
	}
}

func TestScrollHandlerConvertsFromGTKGL(t *testing.T) {
	tests := []struct {
		name         string
		inputPhase   gtkgl.ScrollPhase
		wantPhase    ScrollPhase
		decision     ScrollDecision
		wantDecision gtkgl.ScrollDecision
	}{
		{
			name:         "begin forward",
			inputPhase:   gtkgl.ScrollPhaseBegin,
			wantPhase:    ScrollPhaseBegin,
			decision:     ScrollForwardToCEF,
			wantDecision: gtkgl.ScrollForwardToCEF,
		},
		{
			name:         "update consume",
			inputPhase:   gtkgl.ScrollPhaseUpdate,
			wantPhase:    ScrollPhaseUpdate,
			decision:     ScrollConsume,
			wantDecision: gtkgl.ScrollConsume,
		},
		{
			name:         "end forward",
			inputPhase:   gtkgl.ScrollPhaseEnd,
			wantPhase:    ScrollPhaseEnd,
			decision:     ScrollForwardToCEF,
			wantDecision: gtkgl.ScrollForwardToCEF,
		},
		{
			name:         "decelerate forward",
			inputPhase:   gtkgl.ScrollPhaseDecelerate,
			wantPhase:    ScrollPhaseDecelerate,
			decision:     ScrollForwardToCEF,
			wantDecision: gtkgl.ScrollForwardToCEF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := toGTKGLScrollHandler(func(event ScrollEvent) ScrollDecision {
				if event.Phase != tt.wantPhase {
					t.Fatalf("phase = %v, want %v", event.Phase, tt.wantPhase)
				}
				if event.Unit != gdk.ScrollUnitSurfaceValue {
					t.Fatalf("unit = %v, want surface", event.Unit)
				}
				if !event.UnitKnown {
					t.Fatalf("UnitKnown = false, want true")
				}
				if event.DeltaX != 12 || event.DeltaY != -24 {
					t.Fatalf("deltas = (%d,%d), want (12,-24)", event.DeltaX, event.DeltaY)
				}
				return tt.decision
			})

			got := handler(gtkgl.ScrollEvent{
				Phase:     tt.inputPhase,
				DeltaX:    12,
				DeltaY:    -24,
				Unit:      gdk.ScrollUnitSurfaceValue,
				UnitKnown: true,
			})
			if got != tt.wantDecision {
				t.Fatalf("decision = %v, want %v", got, tt.wantDecision)
			}
		})
	}
}

func TestScrollHandlerNilStaysNil(t *testing.T) {
	if got := toGTKGLScrollHandler(nil); got != nil {
		t.Fatalf("nil handler converted to non-nil")
	}
}

func TestNavigationSwipeHandlerConvertsFromGTKGL(t *testing.T) {
	var got NavigationSwipeAction
	handler := toGTKGLNavigationSwipeHandler(func(action NavigationSwipeAction) {
		got = action
	})

	handler(gtkgl.NavigationSwipeBack)
	if got != NavigationSwipeBack {
		t.Fatalf("action = %v, want back", got)
	}

	handler(gtkgl.NavigationSwipeForward)
	if got != NavigationSwipeForward {
		t.Fatalf("action = %v, want forward", got)
	}
}

func TestNavigationSwipeHandlerNilStaysNil(t *testing.T) {
	if got := toGTKGLNavigationSwipeHandler(nil); got != nil {
		t.Fatalf("nil handler converted to non-nil")
	}
}
