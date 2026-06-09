package gtkgl

import (
	"testing"
	"time"

	"github.com/bnema/purego-cef/cef"
	internalprofile "github.com/bnema/purego-cef2gtk/internal/profile"
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

func TestInputBridgeScrollHandlerCanConsumeUpdate(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var got ScrollEvent
	ib.SetScrollOptions(ScrollOptions{PreciseMultiplier: 2.5}, func(event ScrollEvent) ScrollDecision {
		got = event
		return ScrollConsume
	})

	ib.onScrollUpdate(123, -40, gdk.ScrollUnitSurfaceValue, true, uint(gdk.ShiftMaskValue))

	if got.Phase != ScrollPhaseUpdate {
		t.Fatalf("phase = %v, want update", got.Phase)
	}
	if got.Unit != gdk.ScrollUnitSurfaceValue {
		t.Fatalf("unit = %v, want surface", got.Unit)
	}
	if !got.UnitKnown {
		t.Fatalf("UnitKnown = false, want true for update")
	}
	if got.DeltaX != 308 || got.DeltaY != 100 {
		t.Fatalf("callback deltas = (%d,%d), want (308,100)", got.DeltaX, got.DeltaY)
	}
	if got.Modifiers != uint(gdk.ShiftMaskValue) {
		t.Fatalf("modifiers = %#x, want shift", got.Modifiers)
	}
}

func TestInputBridgeScrollUpdateUsesWheelTranslationWhenUnitUnknown(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var got ScrollEvent
	ib.SetScrollOptions(ScrollOptions{PreciseMultiplier: 2.5}, func(event ScrollEvent) ScrollDecision {
		got = event
		return ScrollConsume
	})

	ib.onScrollUpdate(1, -1, gdk.ScrollUnitSurfaceValue, false, 0)

	if got.Unit != gdk.ScrollUnitSurfaceValue {
		t.Fatalf("reported unit = %v, want original stale surface unit", got.Unit)
	}
	if got.UnitKnown {
		t.Fatalf("UnitKnown = true, want false")
	}
	if got.DeltaX != 240 || got.DeltaY != 240 {
		t.Fatalf("unknown-unit deltas = (%d,%d), want wheel translation (240,240)", got.DeltaX, got.DeltaY)
	}
}

func TestInputBridgeNavigationSwipeRecognizesHorizontalTouchpadBackScroll(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var actions []NavigationSwipeAction
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return true }, func() bool { return false }, func(action NavigationSwipeAction) {
		actions = append(actions, action)
	})

	ib.onScrollUpdate(-201, 1, gdk.ScrollUnitSurfaceValue, true, 0)
	if len(actions) != 0 {
		t.Fatalf("actions before end = %v, want none", actions)
	}
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(actions) != 1 || actions[0] != NavigationSwipeBack {
		t.Fatalf("actions = %v, want one back action", actions)
	}
}

func TestInputBridgeNavigationSwipeRecognizesHorizontalTouchpadForwardScroll(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var actions []NavigationSwipeAction
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return false }, func() bool { return true }, func(action NavigationSwipeAction) {
		actions = append(actions, action)
	})

	ib.onScrollUpdate(201, 1, gdk.ScrollUnitSurfaceValue, true, 0)
	if len(actions) != 0 {
		t.Fatalf("actions before end = %v, want none", actions)
	}
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(actions) != 1 || actions[0] != NavigationSwipeForward {
		t.Fatalf("actions = %v, want one forward action", actions)
	}
}

func TestInputBridgeNavigationSwipeIgnoresMouseWheel(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	called := false
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return true }, func() bool { return true }, func(NavigationSwipeAction) {
		called = true
	})

	ib.onScrollUpdate(8, 0, gdk.ScrollUnitWheelValue, true, 0)
	ib.onScrollUpdate(8, 0, gdk.ScrollUnitWheelValue, true, 0)

	if called {
		t.Fatalf("navigation swipe fired for mouse wheel")
	}
}

func TestInputBridgeNavigationSwipeCancelsVerticalGestures(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	called := false
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true, MinDelta: 15, MaxVerticalRatio: 0.5}, func() bool { return true }, func() bool { return false }, func(NavigationSwipeAction) {
		called = true
	})

	ib.onScrollUpdate(20, 11, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)

	if called {
		t.Fatalf("navigation swipe fired for vertical-dominant gesture")
	}
}

func TestInputBridgeNavigationSwipeVerticalCancelPersistsUntilScrollEnd(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	called := false
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true, MaxVerticalRatio: 0.5}, func() bool { return true }, func() bool { return false }, func(NavigationSwipeAction) {
		called = true
	})

	ib.onScrollUpdate(-100, 60, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(-250, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)

	if called {
		t.Fatalf("navigation swipe fired after vertical cancellation in same gesture")
	}
}

func TestInputBridgeNavigationSwipeBeginClearsInterruptedVerticalCancel(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var actions []NavigationSwipeAction
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true, MaxVerticalRatio: 0.5}, func() bool { return true }, func() bool { return false }, func(action NavigationSwipeAction) {
		actions = append(actions, action)
	})

	ib.onScrollUpdate(-100, 60, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseBegin, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(-250, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(actions) != 1 || actions[0] != NavigationSwipeBack {
		t.Fatalf("actions = %v, want one back action after new scroll begin", actions)
	}
}

func TestInputBridgeNavigationSwipeTracksConsumedScrollUpdates(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	ib.SetScrollOptions(ScrollOptions{}, func(ScrollEvent) ScrollDecision {
		return ScrollConsume
	})
	var actions []NavigationSwipeAction
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return true }, func() bool { return false }, func(action NavigationSwipeAction) {
		actions = append(actions, action)
	})

	ib.onScrollUpdate(-201, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(actions) != 1 || actions[0] != NavigationSwipeBack {
		t.Fatalf("actions = %v, want one back action", actions)
	}
}

func TestInputBridgeNavigationSwipeRequiresCapability(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	called := false
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return false }, func() bool { return false }, func(NavigationSwipeAction) {
		called = true
	})

	ib.onScrollUpdate(-201, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)

	if called {
		t.Fatalf("navigation swipe fired without navigation capability")
	}
}

func TestInputBridgeNavigationSwipeRequiresWebKitCommitDistance(t *testing.T) {
	for _, dx := range []float64{-197.7, -200} {
		ib := NewInputBridge(nil, 1)
		called := false
		ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return true }, func() bool { return false }, func(NavigationSwipeAction) {
			called = true
		})

		ib.onScrollUpdate(dx, 0, gdk.ScrollUnitSurfaceValue, true, 0)
		ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)

		if called {
			t.Fatalf("navigation swipe fired for dx %v, want none", dx)
		}
	}
}

func TestInputBridgeNavigationSwipeResetsOnScrollEnd(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var actions []NavigationSwipeAction
	ib.SetNavigationSwipeHandler(NavigationSwipeOptions{Enabled: true}, func() bool { return true }, func() bool { return false }, func(action NavigationSwipeAction) {
		actions = append(actions, action)
	})

	ib.onScrollUpdate(8, 0, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollBoundary(ScrollPhaseEnd, gdk.ScrollUnitSurfaceValue, true, 0)
	ib.onScrollUpdate(8, 0, gdk.ScrollUnitSurfaceValue, true, 0)

	if len(actions) != 0 {
		t.Fatalf("actions = %v, want none after reset below threshold", actions)
	}
}

func TestInputBridgeRecordsScrollInProfiler(t *testing.T) {
	recorder := internalprofile.NewRecorder()
	start := time.Unix(100, 0)
	recorder.Start(start)
	ib := NewInputBridge(nil, 1)
	ib.SetProfiler(recorder)

	ib.onScrollUpdate(1.5, -2.25, gdk.ScrollUnitWheelValue, true, 0)

	snap, ok := recorder.MaybeSnapshot(start.Add(time.Second), time.Second)
	if !ok {
		t.Fatal("snapshot not emitted")
	}
	if snap.ScrollEvents != 1 || snap.ScrollDXSum != 1.5 || snap.ScrollDYSum != -2.25 {
		t.Fatalf("scroll profile = %+v", snap)
	}
}

func TestTranslateMouseButton(t *testing.T) {
	if got := TranslateMouseButton(2); got != cef.MouseButtonTypeMbtMiddle {
		t.Fatalf("middle button = %v", got)
	}
	if got := TranslateMouseButton(99); got != cef.MouseButtonTypeMbtLeft {
		t.Fatalf("unknown button = %v", got)
	}
}

func TestTranslateModifiers(t *testing.T) {
	mods := uint(gdk.ShiftMaskValue | gdk.ControlMaskValue | gdk.Button1MaskValue)
	got := TranslateModifiers(mods)
	want := uint32(cef.EventFlagsEventflagShiftDown | cef.EventFlagsEventflagControlDown | cef.EventFlagsEventflagLeftMouseButton)
	if got != want {
		t.Fatalf("TranslateModifiers = %#x, want %#x", got, want)
	}
}

func TestGDKKeyvalToWindowsVK(t *testing.T) {
	tests := map[uint]int32{
		'a':    0x41,
		'A':    0x41,
		'7':    0x37,
		'!':    0x31,
		'.':    0xBE,
		0xffbe: 0x70, // GDK_KEY_F1
		0xff51: 0x25, // GDK_KEY_Left
	}
	for keyval, want := range tests {
		if got := GDKKeyvalToWindowsVK(keyval); got != want {
			t.Fatalf("GDKKeyvalToWindowsVK(%#x) = %#x, want %#x", keyval, got, want)
		}
	}
}

func TestBuildMouseEventScaleDefault(t *testing.T) {
	evt := BuildMouseEvent(10.5, 2, 0, 0)
	if evt.X != 10 || evt.Y != 2 {
		t.Fatalf("BuildMouseEvent coords = (%d,%d), want (10,2)", evt.X, evt.Y)
	}
}

func TestBuildMouseEventFractionalScale(t *testing.T) {
	evt := BuildMouseEvent(10.5, 2.25, 0, 1.2)
	if evt.X != 12 || evt.Y != 2 {
		t.Fatalf("BuildMouseEvent coords = (%d,%d), want (12,2)", evt.X, evt.Y)
	}
}

func TestMiddleClickHandlerStoredWithHost(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	called := false
	ib.SetMiddleClickHandler(func(x, y float64) bool {
		called = true
		if x != 10 || y != 20 {
			t.Fatalf("middle click coords=(%v,%v), want (10,20)", x, y)
		}
		return true
	})

	host, scale, handler := ib.currentHostAndMiddleClickHandler()
	if host != nil {
		t.Fatalf("host = %v, want nil", host)
	}
	if scale != 1 {
		t.Fatalf("scale = %v, want 1", scale)
	}
	if handler == nil {
		t.Fatalf("middle click handler nil")
	}
	if !handler(10, 20) || !called {
		t.Fatalf("middle click handler not invoked/consuming")
	}
}

func TestMiddleClickConsumedReleaseOnlyOnce(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	ib.setMiddleClickConsumed(true)
	if !ib.consumeMiddleClickRelease() {
		t.Fatalf("first release was not consumed")
	}
	if ib.consumeMiddleClickRelease() {
		t.Fatalf("second release consumed unexpectedly")
	}
}

func TestClipboardShortcutAction(t *testing.T) {
	if action, ok := clipboardShortcutAction(gdkKeyLowercaseC, uint(gdk.ControlMaskValue)); !ok || action != "copy" {
		t.Fatalf("ctrl-c action=(%q,%v), want copy,true", action, ok)
	}
	if action, ok := clipboardShortcutAction(gdkKeyUppercaseX, uint(gdk.ControlMaskValue)); !ok || action != "cut" {
		t.Fatalf("ctrl-x action=(%q,%v), want cut,true", action, ok)
	}
	if _, ok := clipboardShortcutAction(gdkKeyLowercaseC, uint(gdk.ControlMaskValue|gdk.ShiftMaskValue)); ok {
		t.Fatalf("ctrl-shift-c unexpectedly matched")
	}
}

func TestMirrorClipboardShortcut(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	var gotAction, gotText string
	ib.SetClipboardShortcutHandler(func() string { return "selected" }, func(action, text string) {
		gotAction, gotText = action, text
	})
	ib.mirrorClipboardShortcut(gdkKeyLowercaseC, uint(gdk.ControlMaskValue))
	if gotAction != "copy" || gotText != "selected" {
		t.Fatalf("shortcut=(%q,%q), want copy,selected", gotAction, gotText)
	}
}
