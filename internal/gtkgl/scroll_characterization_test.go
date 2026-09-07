package gtkgl

import (
	"testing"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/puregotk/v4/gdk"
)

// scrollWheelCapture records full wheel submissions including integer deltas.
// The shared recordingBrowserHost discards deltas; characterization needs them
// to pin conversion, modifier, and consume semantics before extraction.
type scrollWheelCapture struct {
	cef.BrowserHost
	events []scrollWheelSubmission
}

type scrollWheelSubmission struct {
	event  cef.MouseEvent
	dx, dy int32
}

func (h *scrollWheelCapture) SendMouseWheelEvent(event *cef.MouseEvent, dx, dy int32) {
	h.events = append(h.events, scrollWheelSubmission{event: *event, dx: dx, dy: dy})
}

func TestScrollCharacterizationCallbackOrdering(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var phases []ScrollPhase
	ib.SetScrollOptions(ScrollOptions{}, func(event ScrollEvent) ScrollDecision {
		phases = append(phases, event.Phase)
		return ScrollForwardToCEF
	})

	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(1, -1, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollDecelerate(10, -20, gdk.ScrollUnitSurfaceValue, true, 0)

	want := []ScrollPhase{ScrollPhaseBegin, ScrollPhaseUpdate, ScrollPhaseEnd, ScrollPhaseDecelerate}
	if len(phases) != len(want) {
		t.Fatalf("callback phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("callback phases = %v, want %v", phases, want)
		}
	}
}

func TestScrollCharacterizationModifiersPreserved(t *testing.T) {
	host := &scrollWheelCapture{}
	ib := NewInputBridge(host, 1)
	ib.SetScrollOptions(ScrollOptions{}, nil)

	ib.onScrollUpdate(1, 0, gdk.ScrollUnitWheelValue, true, uint(gdk.ShiftMaskValue))

	if len(host.events) != 1 {
		t.Fatalf("wheel events = %d, want 1", len(host.events))
	}
	if host.events[0].event.Modifiers&uint32(cef.EventFlagsEventflagShiftDown) == 0 {
		t.Fatalf("wheel modifiers = %#x, want shift bit", host.events[0].event.Modifiers)
	}
}

func TestScrollCharacterizationConsumeSuppressesHostDelivery(t *testing.T) {
	host := &scrollWheelCapture{}
	ib := NewInputBridge(host, 1)
	ib.SetScrollOptions(ScrollOptions{}, func(ScrollEvent) ScrollDecision {
		return ScrollConsume
	})

	ib.onScrollUpdate(2, -2, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(host.events) != 0 {
		t.Fatalf("wheel events = %d, want 0 for consumed update", len(host.events))
	}
}

func TestScrollCharacterizationSurfaceUpdateSetsPrecisionFlag(t *testing.T) {
	host := &scrollWheelCapture{}
	ib := NewInputBridge(host, 1)
	ib.SetScrollOptions(ScrollOptions{}, nil)

	ib.onScrollUpdate(1, -1, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(host.events) != 1 {
		t.Fatalf("wheel events = %d, want 1", len(host.events))
	}
	if host.events[0].event.Modifiers&uint32(cef.EventFlagsEventflagPrecisionScrollingDelta) == 0 {
		t.Fatalf("surface wheel modifiers = %#x, want precision bit", host.events[0].event.Modifiers)
	}
}

func TestScrollCharacterizationWheelUpdateOmitsPrecisionFlag(t *testing.T) {
	host := &scrollWheelCapture{}
	ib := NewInputBridge(host, 1)
	ib.SetScrollOptions(ScrollOptions{}, nil)

	ib.onScrollUpdate(1, -1, gdk.ScrollUnitWheelValue, true, 0)

	if len(host.events) != 1 {
		t.Fatalf("wheel events = %d, want 1", len(host.events))
	}
	if host.events[0].event.Modifiers&uint32(cef.EventFlagsEventflagPrecisionScrollingDelta) != 0 {
		t.Fatalf("wheel modifiers = %#x, want no precision bit", host.events[0].event.Modifiers)
	}
}

func TestScrollCharacterizationStaleBeginUnitDoesNotBlockSurfaceRecognition(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var actions []NavigationSwipeAction
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return true }, func() bool { return false }, func(action NavigationSwipeAction) {
		actions = append(actions, action)
	})

	// Begin reports a stale wheel unit (GTK updates GetUnit only after the
	// first scroll signal); eligibility must come from accepted updates.
	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitWheelValue, true, 0)
	ib.onScrollUpdate(-201, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(actions) != 1 || actions[0] != NavigationSwipeBack {
		t.Fatalf("actions = %v, want one back action despite stale begin unit", actions)
	}
}

func TestScrollCharacterizationHoldWithoutMotionDeliversNothing(t *testing.T) {
	host := &scrollWheelCapture{}
	ib := NewInputBridge(host, 1)
	called := false
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return true }, func() bool { return false }, func(NavigationSwipeAction) {
		called = true
	})

	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(host.events) != 0 {
		t.Fatalf("wheel events = %d, want 0 for hold-only gesture", len(host.events))
	}
	if called {
		t.Fatalf("navigation fired for hold-only gesture")
	}
}

func TestScrollCharacterizationEndBeforeDecelerateIsOrdered(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var phases []ScrollPhase
	ib.SetScrollOptions(ScrollOptions{}, func(event ScrollEvent) ScrollDecision {
		phases = append(phases, event.Phase)
		return ScrollForwardToCEF
	})

	// GTK emits end before decelerate; the bridge must forward both in order
	// without treating the late decelerate as new motion.
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollDecelerate(100, 0, gdk.ScrollUnitSurfaceValue, true, 0)

	want := []ScrollPhase{ScrollPhaseEnd, ScrollPhaseDecelerate}
	if len(phases) != len(want) {
		t.Fatalf("callback phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("callback phases = %v, want %v", phases, want)
		}
	}
}

func TestScrollCharacterizationDecelerateCarriesVelocity(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var got ScrollEvent
	ib.SetScrollOptions(ScrollOptions{}, func(event ScrollEvent) ScrollDecision {
		got = event
		return ScrollForwardToCEF
	})

	ib.onScrollDecelerate(1200, -600, gdk.ScrollUnitSurfaceValue, true, 0)

	if got.Phase != ScrollPhaseDecelerate {
		t.Fatalf("phase = %v, want decelerate", got.Phase)
	}
	if got.VelocityX != 1200 || got.VelocityY != -600 {
		t.Fatalf("velocity = (%v,%v), want (1200,-600)", got.VelocityX, got.VelocityY)
	}
	if !got.UnitKnown || got.Unit != gdk.ScrollUnitSurfaceValue {
		t.Fatalf("unit = (%v,%v), want known surface", got.Unit, got.UnitKnown)
	}
}
