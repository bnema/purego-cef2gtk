package gtkgl

import (
	"math"
	"testing"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/puregotk/v4/gdk"
)

func newAnimatedTestBridge(handler func(ScrollEvent) ScrollDecision, opts ScrollOptions) (*InputBridge, *scrollWheelCapture, *gateRecorder) {
	host := &scrollWheelCapture{}
	rec := &gateRecorder{}
	ib := NewInputBridge(host, 1)
	ib.scroll.sender = rec.send
	ib.SetScrollOptions(opts, handler)
	return ib, host, rec
}

func forwardCounter(counts map[ScrollPhase]int) func(ScrollEvent) ScrollDecision {
	return func(event ScrollEvent) ScrollDecision {
		counts[event.Phase]++
		return ScrollForwardToCEF
	}
}

func stepReleaseToEnd(ib *InputBridge) {
	t := ib.scroll.session.t0
	for i := 0; i < 10000; i++ {
		t += 1.0 / 60
		if !ib.scroll.step(t) {
			return
		}
	}
}

func TestBridgeAnimatedTouchpadDirectAndRelease(t *testing.T) {
	counts := map[ScrollPhase]int{}
	ib, host, rec := newAnimatedTestBridge(forwardCounter(counts), ScrollOptions{TouchpadInertia: true})

	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(10, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(10, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollDecelerate(800, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	stepReleaseToEnd(ib)

	// Physical tracking stays direct (2x25 units) and the release decays
	// (~360 units); everything flows through the gate, never the raw host.
	if len(host.events) != 0 {
		t.Fatalf("raw host deliveries = %d, want 0 (all gated)", len(host.events))
	}
	var total int64
	for _, s := range rec.subs {
		total += int64(s.dx)
	}
	if math.Abs(float64(total)-410) > 6 {
		t.Fatalf("gated total = %d, want near 410 (50 direct + 360 release)", total)
	}
	want := map[ScrollPhase]int{ScrollPhaseBegin: 1, ScrollPhaseUpdate: 2, ScrollPhaseEnd: 1, ScrollPhaseDecelerate: 1}
	for phase, n := range want {
		if counts[phase] != n {
			t.Fatalf("handler phase %v = %d, want %d (synthetic never reenters OnScroll)", phase, counts[phase], n)
		}
	}
}

func TestBridgeHandlerCancelSuppressesPhysical(t *testing.T) {
	var ib *InputBridge
	var retiredBegin, retiredUpdate uint64
	ib, host, rec := newAnimatedTestBridge(func(event ScrollEvent) ScrollDecision {
		if event.Phase == ScrollPhaseBegin {
			retiredBegin = ib.InvalidateScroll()
		} else {
			retiredUpdate = ib.InvalidateScroll()
		}
		return ScrollForwardToCEF
	}, ScrollOptions{TouchpadInertia: true})

	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(10, 0, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(rec.subs) != 0 || len(host.events) != 0 {
		t.Fatalf("cancelled update delivered: gated=%d raw=%d, want 0/0", len(rec.subs), len(host.events))
	}
	// The session is retired (stale) the moment invalidation returns; the
	// epoch-scoped cleanup then clears it without touching newer state.
	if ib.scroll.session.epoch == ib.scroll.epoch.Load() {
		t.Fatal("session still live after cancel")
	}
	if !ib.CancelScrollEpoch(retiredBegin) {
		t.Fatal("retired-epoch cleanup reported failure")
	}
	if ib.CancelScrollEpoch(retiredUpdate) {
		t.Fatal("empty-epoch cleanup reported success")
	}
	if ib.scroll.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want cleared after cleanup", ib.scroll.session.kind)
	}
	// Later phases of the dead gesture stay silent too.
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollDecelerate(900, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	stepReleaseToEnd(ib)
	if len(rec.subs) != 0 {
		t.Fatalf("dead gesture submitted %d events", len(rec.subs))
	}
}

func TestBridgeHandlerConsumePoisonsRelease(t *testing.T) {
	ib, host, rec := newAnimatedTestBridge(func(event ScrollEvent) ScrollDecision {
		if event.Phase == ScrollPhaseUpdate {
			return ScrollConsume
		}
		return ScrollForwardToCEF
	}, ScrollOptions{TouchpadInertia: true})

	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(10, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollDecelerate(900, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	stepReleaseToEnd(ib)

	if len(rec.subs) != 0 || len(host.events) != 0 {
		t.Fatalf("consumed gesture delivered: gated=%d raw=%d, want 0/0", len(rec.subs), len(host.events))
	}
}

func TestBridgeModifiedInputBypassesAnimation(t *testing.T) {
	ib, host, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}),
		ScrollOptions{TouchpadInertia: true, WheelSmoothing: true})

	ib.onScrollUpdate(10, 0, gdk.ScrollUnitSurfaceValue, true, uint(gdk.ControlMaskValue))

	if len(host.events) != 1 {
		t.Fatalf("raw host deliveries = %d, want 1 direct zoom delivery", len(host.events))
	}
	if len(rec.subs) != 0 {
		t.Fatalf("gated submissions = %d, want 0 for modified input", len(rec.subs))
	}
	if ib.scroll.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want none for modified input", ib.scroll.session.kind)
	}
	if host.events[0].event.Modifiers&uint32(cef.EventFlagsEventflagControlDown) == 0 {
		t.Fatalf("modifiers = %#x, want control bit preserved", host.events[0].event.Modifiers)
	}
}

func TestBridgeDisabledOptionsPreserveDirect(t *testing.T) {
	ib, host, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{})

	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(10, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollDecelerate(900, 0, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(host.events) != 1 {
		t.Fatalf("raw host deliveries = %d, want 1 legacy update", len(host.events))
	}
	if len(rec.subs) != 0 {
		t.Fatalf("gated submissions = %d, want 0 when disabled", len(rec.subs))
	}
	if ib.scroll.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want none when disabled", ib.scroll.session.kind)
	}
}

func TestBridgeNavigationSuppressesRelease(t *testing.T) {
	var actions []NavigationSwipeAction
	ib, _, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{TouchpadInertia: true})
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return true }, func() bool { return false }, func(action NavigationSwipeAction) {
		actions = append(actions, action)
	})

	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(-150, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(-150, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollDecelerate(900, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	stepReleaseToEnd(ib)

	if len(actions) != 1 || actions[0] != NavigationSwipeBack {
		t.Fatalf("nav actions = %v, want one back", actions)
	}
	// Only the two direct updates (-375 each) were gated; the navigating
	// swipe never flings into the new page.
	var total int64
	for _, s := range rec.subs {
		total += int64(s.dx)
	}
	if total != -750 {
		t.Fatalf("gated total = %d, want -750 (direct only, no release)", total)
	}
}

func TestBridgeSyntheticWheelNeverFeedsHandlerOrNav(t *testing.T) {
	counts := map[ScrollPhase]int{}
	navFired := false
	ib, _, rec := newAnimatedTestBridge(forwardCounter(counts), ScrollOptions{WheelSmoothing: true})
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return true }, func() bool { return true }, func(NavigationSwipeAction) {
		navFired = true
	})

	for i := 0; i < 3; i++ {
		ib.onScrollUpdate(0, -0.2, gdk.ScrollUnitWheelValue, true, 0)
	}
	lastImpulse := ib.scroll.session.lastImpulseT
	for ts := lastImpulse; ts < lastImpulse+1; ts += 1.0 / 60 {
		if !ib.scroll.step(ts) {
			break
		}
	}

	if counts[ScrollPhaseUpdate] != 3 {
		t.Fatalf("handler updates = %d, want 3 physical only", counts[ScrollPhaseUpdate])
	}
	if navFired {
		t.Fatal("synthetic wheel output fed navigation recognition")
	}
	if len(rec.subs) == 0 {
		t.Fatal("smoothed burst submitted nothing")
	}
	for _, s := range rec.subs {
		if s.evt.Modifiers&uint32(cef.EventFlagsEventflagPrecisionScrollingDelta) == 0 {
			t.Fatalf("synthetic modifiers = %#x, want precision bit", s.evt.Modifiers)
		}
	}
}

func TestBridgeDetachCancelsRelease(t *testing.T) {
	ib, _, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{TouchpadInertia: true})

	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(10, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollDecelerate(800, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	before := len(rec.subs)
	ib.Detach()
	if ib.scroll.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want cleared on detach", ib.scroll.session.kind)
	}
	stepReleaseToEnd(ib)
	if len(rec.subs) != before {
		t.Fatalf("submissions after detach = %d, want %d", len(rec.subs), before)
	}
}

func TestBridgeSetHostReplacementCancels(t *testing.T) {
	ib, _, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{TouchpadInertia: true})

	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(10, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollDecelerate(800, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	before := len(rec.subs)
	ib.SetHost(&scrollWheelCapture{})
	if ib.scroll.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want cleared on host replacement", ib.scroll.session.kind)
	}
	stepReleaseToEnd(ib)
	if len(rec.subs) != before {
		t.Fatalf("submissions after host replacement = %d, want %d", len(rec.subs), before)
	}
}

func TestBridgePressCancelsRelease(t *testing.T) {
	ib, _, rec := newAnimatedTestBridge(forwardCounter(map[ScrollPhase]int{}), ScrollOptions{TouchpadInertia: true})

	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(10, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollDecelerate(800, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	before := len(rec.subs)
	ib.onMousePress(5, 5, 1, 0, 1)
	if ib.scroll.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want cleared on press", ib.scroll.session.kind)
	}
	stepReleaseToEnd(ib)
	if len(rec.subs) != before {
		t.Fatalf("submissions after press = %d, want %d", len(rec.subs), before)
	}
}
