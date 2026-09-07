package gtkgl

import (
	"testing"

	"github.com/bnema/puregotk/v4/gdk"
)

func TestScrollTracerDisabledByDefault(t *testing.T) {
	t.Setenv(scrollTraceEnv, "")
	ib := NewInputBridge(nil, 1)
	if ib.scroll.tracer != nil {
		t.Fatal("tracer enabled without opt-in")
	}
	// Nil tracer is a silent no-op.
	ib.scroll.tracef("invisible %d", 1)
}

func TestScrollTracerEnabledAndBounded(t *testing.T) {
	t.Setenv(scrollTraceEnv, "1")
	ib := NewInputBridge(nil, 1)
	if ib.scroll.tracer == nil {
		t.Fatal("tracer not enabled by opt-in")
	}
	for i := 0; i < scrollTraceMaxLines+50; i++ {
		ib.scroll.tracef("line %d", i)
	}
	if ib.scroll.tracer.lines != scrollTraceMaxLines {
		t.Fatalf("traced lines = %d, want cap %d", ib.scroll.tracer.lines, scrollTraceMaxLines)
	}
	if !ib.scroll.tracer.truncated {
		t.Fatal("truncation not flagged")
	}
}

func TestMonotonicClockNeverGoesBackward(t *testing.T) {
	last := wallClockSeconds()
	for i := 0; i < 1000; i++ {
		now := wallClockSeconds()
		if now < last {
			t.Fatalf("clock went backward: %v -> %v", last, now)
		}
		last = now
	}
}

func TestBridgeUnknownUnitUsesWheelTranslation(t *testing.T) {
	ib, _, _ := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{WheelSmoothing: true})
	// Raw unit reports surface but unknown: floats must follow the same
	// wheel fallback as the integer path, not precise surface scaling.
	ib.onScrollUpdate(1, -1, gdk.ScrollUnitSurfaceValue, false, 0)
	if ib.scroll.session.pendingX != 240 || ib.scroll.session.pendingY != 240 {
		t.Fatalf("pending = (%v,%v), want wheel translation (240,240)",
			ib.scroll.session.pendingX, ib.scroll.session.pendingY)
	}
}

func TestBridgeBeginPreservesWheelBurst(t *testing.T) {
	ib, _, _ := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{TouchpadInertia: true, WheelSmoothing: true})
	ib.onScrollUpdate(0, -1, gdk.ScrollUnitWheelValue, true, 0)
	pendingBefore := ib.scroll.session.pendingX
	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitWheelValue, true, 0)
	if ib.scroll.session.kind != scrollSessionWheel || !ib.scroll.session.burstActive {
		t.Fatalf("begin discarded wheel burst: %+v", ib.scroll.session)
	}
	if ib.scroll.session.pendingX != pendingBefore {
		t.Fatalf("burst pending changed across begin: %v -> %v", pendingBefore, ib.scroll.session.pendingX)
	}
}
