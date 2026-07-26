package gtkgl

import (
	"math"
	"testing"
)

func TestPointerTrackerClaimsOnceWhenHorizontalMotionCrossesThreshold(t *testing.T) {
	claims := 0
	tracker := NewPointerTracker(8, func() { claims++ }, nil)

	tracker.Press(10, 20, 1, 0x10)
	runTrackerMotion(tracker, 18, 20, 0x11)
	if claims != 0 {
		t.Fatalf("claims at exact threshold = %d, want 0", claims)
	}
	runTrackerMotion(tracker, 19, 20, 0x11)
	runTrackerMotion(tracker, 30, 20, 0x11)

	if claims != 1 {
		t.Fatalf("claims after crossing threshold = %d, want 1", claims)
	}
	if tracker.Phase() != PointerDragging {
		t.Fatalf("phase = %v, want dragging", tracker.Phase())
	}
}

func TestPointerTrackerClaimsWhenVerticalMotionCrossesThreshold(t *testing.T) {
	claims := 0
	tracker := NewPointerTracker(8, func() { claims++ }, nil)
	tracker.Press(10, 20, 1, 0)

	runTrackerMotion(tracker, 10, 29, 0)

	if claims != 1 || tracker.Phase() != PointerDragging {
		t.Fatalf("claims, phase = %d, %v; want 1, dragging", claims, tracker.Phase())
	}
}

func TestPointerTrackerDiagonalMotionUsesPerAxisThreshold(t *testing.T) {
	claims := 0
	tracker := NewPointerTracker(8, func() { claims++ }, nil)
	tracker.Press(0, 0, 1, 0)

	tracker.Motion(7, 7, 0)
	tracker.Motion(-8, -8, 0)

	if claims != 0 || tracker.Phase() != PointerPressed {
		t.Fatalf("claims, phase = %d, %v; want 0, pressed", claims, tracker.Phase())
	}
}

func TestPointerTrackerThresholdRequiresStrictlyGreaterAxisMotion(t *testing.T) {
	tests := []struct {
		name string
		x    float64
		y    float64
		want bool
	}{
		{name: "positive X equality", x: 8},
		{name: "negative X equality", x: -8},
		{name: "positive Y equality", y: 8},
		{name: "negative Y equality", y: -8},
		{name: "X greater", x: 8.01, want: true},
		{name: "Y greater", y: -8.01, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := 0
			tracker := NewPointerTracker(8, func() { claims++ }, nil)
			tracker.Press(0, 0, 1, 0)
			runTrackerMotion(tracker, tt.x, tt.y, 0)
			if got := claims == 1; got != tt.want {
				t.Fatalf("claimed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPointerTrackerInvalidThresholdFallsBackToEight(t *testing.T) {
	for _, threshold := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		claims := 0
		tracker := NewPointerTracker(threshold, func() { claims++ }, nil)
		tracker.Press(0, 0, 1, 0)
		runTrackerMotion(tracker, 8, 0, 0)
		runTrackerMotion(tracker, 8.01, 0, 0)
		if claims != 1 {
			t.Fatalf("threshold %v produced %d claims, want fallback claim after 8", threshold, claims)
		}
	}
}

func TestPointerTrackerTracksNegativeAndOutOfBoundsCoordinates(t *testing.T) {
	tracker := NewPointerTracker(8, nil, nil)
	tracker.Press(-20, 5000, 1, 0x10)
	tracker.Motion(-30, 7000, 0x21)

	if tracker.pressX != -20 || tracker.pressY != 5000 {
		t.Fatalf("press coords = (%v,%v), want (-20,5000)", tracker.pressX, tracker.pressY)
	}
	if tracker.lastX != -30 || tracker.lastY != 7000 || !tracker.coordsValid {
		t.Fatalf("last coords/valid = (%v,%v,%v), want (-30,7000,true)", tracker.lastX, tracker.lastY, tracker.coordsValid)
	}
	if tracker.lastState != 0x21 {
		t.Fatalf("last state = %#x, want %#x", tracker.lastState, uint(0x21))
	}
}

func TestPointerTrackerStartsWithoutValidCoordinates(t *testing.T) {
	tracker := NewPointerTracker(8, nil, nil)
	if tracker.coordsValid {
		t.Fatal("coordsValid = true before a real coordinate sample")
	}

	tracker.Press(0, 0, 1, 0)
	if !tracker.coordsValid {
		t.Fatal("coordsValid = false after press")
	}
}

func TestPointerTrackerKeepsFirstButtonDuringMultiButtonInteraction(t *testing.T) {
	claims := 0
	tracker := NewPointerTracker(8, func() { claims++ }, nil)
	tracker.Press(10, 10, 1, 0x01)
	tracker.Press(40, 40, 3, 0x05)

	if tracker.button != 1 || tracker.pressX != 10 || tracker.pressY != 10 {
		t.Fatalf("tracked button/press = %d,(%v,%v), want 1,(10,10)", tracker.button, tracker.pressX, tracker.pressY)
	}
	tracker.Release(40, 40, 3, 0x01)
	if tracker.Phase() != PointerPressed {
		t.Fatalf("phase after other-button release = %v, want pressed", tracker.Phase())
	}
	runTrackerMotion(tracker, 19, 10, 0x01)
	if claims != 1 {
		t.Fatalf("claims = %d, want 1 from first button's press origin", claims)
	}
	tracker.Release(19, 10, 1, 0)
	if tracker.Phase() != PointerIdle {
		t.Fatalf("phase after tracked-button release = %v, want idle", tracker.Phase())
	}
}

func TestPointerTrackerNormalCancelReportsLastRealStateOnce(t *testing.T) {
	var aborts []PointerAbort
	tracker := NewPointerTracker(8, nil, func(abort PointerAbort) {
		aborts = append(aborts, abort)
	})
	tracker.Press(-10, 20, 2, 0x22)
	tracker.Motion(-30, 45, 0x37)

	tracker.Cancel()
	tracker.Cancel()

	if tracker.Phase() != PointerIdle {
		t.Fatalf("phase = %v, want idle", tracker.Phase())
	}
	if len(aborts) != 1 {
		t.Fatalf("abort count = %d, want 1", len(aborts))
	}
	want := PointerAbort{Button: 2, X: -30, Y: 45, CoordsValid: true, State: 0x37}
	if aborts[0] != want {
		t.Fatalf("abort = %+v, want %+v", aborts[0], want)
	}
}

func runTrackerMotion(tracker *PointerTracker, x, y float64, state uint) {
	if claim := tracker.Motion(x, y, state); claim != nil {
		claim()
	}
}

func TestPointerTrackerDisarmDndRetiresOwnedInteractionSilently(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*PointerTracker)
	}{
		{
			name: "pressed without GTK cancel",
			setup: func(tracker *PointerTracker) {
				tracker.Press(10, 20, 1, 0x01)
				tracker.ArmDnd()
			},
		},
		{
			name: "dragging without GTK cancel",
			setup: func(tracker *PointerTracker) {
				tracker.Press(10, 20, 1, 0x01)
				runTrackerMotion(tracker, 19, 20, 0x01)
				tracker.ArmDnd()
			},
		},
		{
			name: "GTK cancel during native drag",
			setup: func(tracker *PointerTracker) {
				tracker.Press(10, 20, 1, 0x01)
				tracker.ArmDnd()
				tracker.Cancel()
			},
		},
		{
			name: "idle",
			setup: func(tracker *PointerTracker) {
				tracker.ArmDnd()
			},
		},
		{
			name: "repeated disarm",
			setup: func(tracker *PointerTracker) {
				tracker.Press(10, 20, 1, 0x01)
				tracker.ArmDnd()
				tracker.DisarmDnd()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aborts := 0
			tracker := NewPointerTracker(8, nil, func(PointerAbort) { aborts++ })
			tt.setup(tracker)

			tracker.DisarmDnd()

			if tracker.Phase() != PointerIdle || tracker.dndArmed || tracker.dndCanceled {
				t.Fatalf("state after DisarmDnd = phase:%v armed:%v canceled:%v, want idle,false,false", tracker.Phase(), tracker.dndArmed, tracker.dndCanceled)
			}
			if aborts != 0 {
				t.Fatalf("aborts after DisarmDnd = %d, want 0", aborts)
			}
		})
	}
}

func TestPointerTrackerDndSuspendsCancelRecoveryUntilDisarmed(t *testing.T) {
	aborts := 0
	tracker := NewPointerTracker(8, nil, func(PointerAbort) { aborts++ })
	tracker.Press(10, 20, 1, 0x01)
	tracker.ArmDnd()

	tracker.Cancel()

	if aborts != 0 {
		t.Fatalf("aborts while DnD armed = %d, want 0", aborts)
	}
	if tracker.Phase() != PointerPressed {
		t.Fatalf("phase during DnD-suspended cancel = %v, want pressed", tracker.Phase())
	}
	if !tracker.dndArmed || !tracker.dndCanceled {
		t.Fatalf("DnD state = armed:%v canceled:%v, want true,true", tracker.dndArmed, tracker.dndCanceled)
	}

	tracker.DisarmDnd()

	if aborts != 0 {
		t.Fatalf("aborts after DnD completion = %d, want 0", aborts)
	}
	if tracker.Phase() != PointerIdle || tracker.dndArmed || tracker.dndCanceled {
		t.Fatalf("state after DisarmDnd = phase:%v armed:%v canceled:%v, want idle,false,false", tracker.Phase(), tracker.dndArmed, tracker.dndCanceled)
	}
}
