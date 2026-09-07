package gtkgl

import (
	"testing"

	"github.com/bnema/puregotk/v4/gdk"
)

func TestTranslateScrollDeltas(t *testing.T) {
	x, y := TranslateScrollDeltas(1.5, -2)
	if x != 360 || y != 480 {
		t.Fatalf("TranslateScrollDeltas = (%d,%d), want (360,480)", x, y)
	}
}

func TestTranslateScrollDeltasWithOptionsDefaultsToLegacyBehavior(t *testing.T) {
	x, y := TranslateScrollDeltasWithOptions(1.5, -2, gdk.ScrollUnitWheelValue, ScrollOptions{})
	if x != 360 || y != 480 {
		t.Fatalf("TranslateScrollDeltasWithOptions = (%d,%d), want (360,480)", x, y)
	}
}

func TestTranslateScrollDeltasWithOptionsKeepsLegacyWheelTruncation(t *testing.T) {
	x, y := TranslateScrollDeltasWithOptions(0.003, -0.003, gdk.ScrollUnitWheelValue, ScrollOptions{})
	if x != 0 || y != 0 {
		t.Fatalf("fractional wheel deltas = (%d,%d), want legacy truncation (0,0)", x, y)
	}
}

func TestTranslateScrollDeltasWithOptionsUsesPreciseMultiplierForSurfaceUnits(t *testing.T) {
	x, y := TranslateScrollDeltasWithOptions(123, -40, gdk.ScrollUnitSurfaceValue, ScrollOptions{
		PreciseMultiplier: 2.5,
	})
	if x != 308 || y != 100 {
		t.Fatalf("precise deltas = (%d,%d), want scaled surface pixels (308,100)", x, y)
	}
}

func TestTranslateScrollDeltasWithOptionsDefaultsSurfaceUnitsToWebKitGTKScale(t *testing.T) {
	x, y := TranslateScrollDeltasWithOptions(1.6, -1.6, gdk.ScrollUnitSurfaceValue, ScrollOptions{})
	if x != 4 || y != 4 {
		t.Fatalf("surface pixel deltas = (%d,%d), want WebKitGTK-like scale (4,4)", x, y)
	}
}

func TestTranslateScrollDeltasWithOptionsAppliesAxisMultipliersAndClamp(t *testing.T) {
	x, y := TranslateScrollDeltasWithOptions(2, -2, gdk.ScrollUnitWheelValue, ScrollOptions{
		HorizontalMultiplier: 0.5,
		VerticalMultiplier:   2,
		MaxDelta:             300,
	})
	if x != 240 || y != 300 {
		t.Fatalf("scaled/clamped deltas = (%d,%d), want (240,300)", x, y)
	}
}

func TestScrollControllerSnapshotIsolatesOptions(t *testing.T) {
	c := newScrollController()
	c.setOptions(ScrollOptions{WheelMultiplier: 2}, nil)
	opts, _ := c.snapshot()
	if opts.WheelMultiplier != 2 {
		t.Fatalf("snapshot multiplier = %v, want 2", opts.WheelMultiplier)
	}
}

func TestScrollControllerNilReceiverIsSafe(t *testing.T) {
	var c *scrollController
	c.setOptions(ScrollOptions{}, nil)
	c.setNavigationHandler(NavigationSwipeOptions{}, nil, nil, nil)
	c.setNavState(navigationSwipeState{})
	c.resetNav()
	if _, fn := c.snapshot(); fn != nil {
		t.Fatalf("nil snapshot handler = non-nil, want nil")
	}
	if state := c.navState(); state.cumulativeDX != 0 {
		t.Fatalf("nil nav state = %+v, want zero", state)
	}
}

func TestScrollControllerResetNavClearsSession(t *testing.T) {
	c := newScrollController()
	c.setNavState(navigationSwipeState{cumulativeDX: 10, cumulativeDY: 5, recognized: true, verticalCanceled: true})
	c.resetNav()
	state := c.navState()
	if state.cumulativeDX != 0 || state.cumulativeDY != 0 || state.recognized || state.verticalCanceled {
		t.Fatalf("reset nav state = %+v, want zeroed session", state)
	}
}

func TestInputBridgeDelegatesScrollOptionsToController(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	if ib.scroll == nil {
		t.Fatal("bridge scroll controller is nil")
	}
	ib.SetScrollOptions(ScrollOptions{WheelMultiplier: 3}, nil)
	opts, _ := ib.scroll.snapshot()
	if opts.WheelMultiplier != 3 {
		t.Fatalf("delegated multiplier = %v, want 3", opts.WheelMultiplier)
	}
}
