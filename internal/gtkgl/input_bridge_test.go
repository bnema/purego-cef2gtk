package gtkgl

import (
	"strings"
	"testing"
	"time"

	"github.com/bnema/purego-cef/cef"
	internalprofile "github.com/bnema/purego-cef2gtk/internal/profile"
	"github.com/bnema/puregotk/v4/gdk"
)

type recordingBrowserHost struct {
	cef.BrowserHost
	browser       cef.Browser
	moves         []recordedMouseMove
	clicks        []recordedMouseClick
	captureLosts  int
	hiddenStates  []int32
	focusStates   []int32
	invalidations []cef.PaintElementType
	wheels        []cef.MouseEvent
}

type recordedMouseMove struct {
	event cef.MouseEvent
	leave int32
}

type recordedMouseClick struct {
	event   cef.MouseEvent
	button  cef.MouseButtonType
	mouseUp int32
	count   int32
}

type recordingBrowser struct {
	cef.Browser
	focusedFrame cef.Frame
	mainFrame    cef.Frame
}

type recordingFrame struct {
	cef.Frame
	scripts []string
}

func (h *recordingBrowserHost) GetBrowser() cef.Browser { return h.browser }

func (b *recordingBrowser) GetFocusedFrame() cef.Frame { return b.focusedFrame }
func (b *recordingBrowser) GetMainFrame() cef.Frame    { return b.mainFrame }

func (f *recordingFrame) ExecuteJavaScript(code, _ string, _ int32) {
	f.scripts = append(f.scripts, code)
}

func (h *recordingBrowserHost) SendMouseMoveEvent(event *cef.MouseEvent, leave int32) {
	h.moves = append(h.moves, recordedMouseMove{event: *event, leave: leave})
}

func (h *recordingBrowserHost) SendMouseClickEvent(event *cef.MouseEvent, button cef.MouseButtonType, mouseUp, count int32) {
	h.clicks = append(h.clicks, recordedMouseClick{event: *event, button: button, mouseUp: mouseUp, count: count})
}

func (h *recordingBrowserHost) SendCaptureLostEvent() {
	h.captureLosts++
}

func (h *recordingBrowserHost) SendMouseWheelEvent(event *cef.MouseEvent, _, _ int32) {
	h.wheels = append(h.wheels, *event)
}

func (h *recordingBrowserHost) WasHidden(hidden int32) {
	h.hiddenStates = append(h.hiddenStates, hidden)
}

func (h *recordingBrowserHost) SetFocus(focused int32) {
	h.focusStates = append(h.focusStates, focused)
}

func (h *recordingBrowserHost) Invalidate(element cef.PaintElementType) {
	h.invalidations = append(h.invalidations, element)
}

func TestInputBridgeDetachReleasesPointerTrackerCallbacksAndInteraction(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	claims := 0
	tracker := NewPointerTracker(8, func() { claims++ }, func(PointerAbort) { claims++ })
	ib.pointerTracker = tracker
	tracker.Press(0, 0, 1, 0)

	ib.Detach()

	if tracker.onDragStart != nil || tracker.onAborted != nil {
		t.Fatal("Detach retained pointer tracker callbacks")
	}
	if tracker.Phase() != PointerIdle {
		t.Fatalf("tracker phase after Detach = %v, want idle", tracker.Phase())
	}
	if claim := tracker.Motion(9, 0, 0); claim != nil {
		claim()
	}
	if claims != 0 {
		t.Fatalf("callbacks after Detach = %d, want 0", claims)
	}
}

func TestInputBridgeAlreadyFocusedLeftMouseDownDoesNotResynchronizeBrowser(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)
	ib.onFocusIn()
	host.hiddenStates = nil
	host.focusStates = nil
	host.invalidations = nil

	ib.onMousePress(10, 20, 1, 0, 1)

	if len(host.hiddenStates) != 0 || len(host.focusStates) != 0 || len(host.invalidations) != 0 {
		t.Fatalf("focused mousedown visibility/focus/invalidations = %v/%v/%v, want none", host.hiddenStates, host.focusStates, host.invalidations)
	}
}

func TestInputBridgeFocusWithUnknownVisibilityDoesNotUnhideBrowser(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)

	ib.onFocusIn()
	ib.onFocusIn()
	ib.onFocusOut()
	ib.onFocusOut()

	if len(host.hiddenStates) != 0 {
		t.Fatalf("focus transition hidden states = %v, want none while visibility is unknown", host.hiddenStates)
	}
	if len(host.focusStates) != 2 || host.focusStates[0] != 1 || host.focusStates[1] != 0 {
		t.Fatalf("focus transition states = %v, want [1 0]", host.focusStates)
	}
	if len(host.invalidations) != 1 || host.invalidations[0] != cef.PaintElementTypePetView {
		t.Fatalf("focus transition invalidations = %v, want one view invalidation", host.invalidations)
	}
}

func TestInputBridgeFocusPreservesKnownHiddenVisibility(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)
	ib.SetVisible(false)

	ib.onFocusIn()

	if len(host.hiddenStates) != 1 || host.hiddenStates[0] != 1 {
		t.Fatalf("hidden focus states = %v, want [1]", host.hiddenStates)
	}
	if len(host.focusStates) != 1 || host.focusStates[0] != 1 || len(host.invalidations) != 1 {
		t.Fatalf("hidden focus delivery = %v/%v, want focus-in and invalidation", host.focusStates, host.invalidations)
	}
}

func TestInputBridgeFocusCombinesKnownUndeliveredVisibleState(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	ib.SetVisible(true)
	ib.onFocusIn()
	host := &recordingBrowserHost{}

	ib.SetHost(host)

	if len(host.hiddenStates) != 1 || host.hiddenStates[0] != 0 {
		t.Fatalf("visible focus hidden states = %v, want [0]", host.hiddenStates)
	}
	if len(host.focusStates) != 1 || host.focusStates[0] != 1 || len(host.invalidations) != 1 {
		t.Fatalf("visible focus delivery = %v/%v, want focus-in and invalidation", host.focusStates, host.invalidations)
	}
}

func TestInputBridgeSynchronizesVisibilityOnReattachment(t *testing.T) {
	firstHost := &recordingBrowserHost{}
	first := NewInputBridge(firstHost, 1)
	first.syncWidgetVisibility(false, true)
	first.Detach()

	replacementHost := &recordingBrowserHost{}
	replacement := NewInputBridge(replacementHost, 1)
	replacement.syncWidgetVisibility(true, true)

	if len(firstHost.hiddenStates) != 1 || firstHost.hiddenStates[0] != 1 {
		t.Fatalf("first attachment visibility = %v, want [1]", firstHost.hiddenStates)
	}
	if len(replacementHost.hiddenStates) != 1 || replacementHost.hiddenStates[0] != 0 {
		t.Fatalf("replacement attachment visibility = %v, want [0]", replacementHost.hiddenStates)
	}
}

func TestInputBridgeVisibilityRequiresMappedAndVisibleWidget(t *testing.T) {
	for _, state := range []struct {
		mapped, visible bool
		want            int32
	}{
		{mapped: false, visible: false, want: 1},
		{mapped: false, visible: true, want: 1},
		{mapped: true, visible: false, want: 1},
		{mapped: true, visible: true, want: 0},
	} {
		host := &recordingBrowserHost{}
		NewInputBridge(host, 1).syncWidgetVisibility(state.mapped, state.visible)
		if len(host.hiddenStates) != 1 || host.hiddenStates[0] != state.want {
			t.Fatalf("mapped=%v visible=%v hidden states = %v, want [%d]", state.mapped, state.visible, host.hiddenStates, state.want)
		}
	}
}

func TestInputBridgeVisibilityTransitionsIndependentlyNotifyBrowserOnce(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)

	ib.SetVisible(false)
	ib.SetVisible(false)
	ib.SetVisible(true)
	ib.SetVisible(true)

	if len(host.hiddenStates) != 2 || host.hiddenStates[0] != 1 || host.hiddenStates[1] != 0 {
		t.Fatalf("visibility hidden states = %v, want [1 0]", host.hiddenStates)
	}
	if len(host.focusStates) != 0 || len(host.invalidations) != 0 {
		t.Fatalf("visibility focus/invalidations = %v/%v, want none", host.focusStates, host.invalidations)
	}
}

func TestInputBridgeSetHostReplaysKnownVisibleFocusedStateOncePerAttachment(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	ib.SetVisible(true)
	ib.onFocusIn()

	first := &recordingBrowserHost{}
	ib.SetHost(first)
	ib.SetHost(first)

	if len(first.hiddenStates) != 1 || first.hiddenStates[0] != 0 {
		t.Fatalf("first host hidden states = %v, want [0]", first.hiddenStates)
	}
	if len(first.focusStates) != 1 || first.focusStates[0] != 1 {
		t.Fatalf("first host focus states = %v, want [1]", first.focusStates)
	}
	if len(first.invalidations) != 1 || first.invalidations[0] != cef.PaintElementTypePetView {
		t.Fatalf("first host invalidations = %v, want one view invalidation", first.invalidations)
	}

	replacement := &recordingBrowserHost{}
	ib.SetHost(replacement)

	if len(replacement.hiddenStates) != 1 || replacement.hiddenStates[0] != 0 {
		t.Fatalf("replacement host hidden states = %v, want [0]", replacement.hiddenStates)
	}
	if len(replacement.focusStates) != 1 || replacement.focusStates[0] != 1 {
		t.Fatalf("replacement host focus states = %v, want [1]", replacement.focusStates)
	}
	if len(replacement.invalidations) != 1 || replacement.invalidations[0] != cef.PaintElementTypePetView {
		t.Fatalf("replacement host invalidations = %v, want one view invalidation", replacement.invalidations)
	}
}

func TestInputBridgeSetHostReplaysKnownHiddenUnfocusedStateOnce(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	ib.SetVisible(false)
	ib.onFocusOut()

	host := &recordingBrowserHost{}
	ib.SetHost(host)
	ib.SetVisible(false)
	ib.onFocusOut()

	if len(host.hiddenStates) != 1 || host.hiddenStates[0] != 1 {
		t.Fatalf("hidden states = %v, want [1]", host.hiddenStates)
	}
	if len(host.focusStates) != 1 || host.focusStates[0] != 0 {
		t.Fatalf("focus states = %v, want [0]", host.focusStates)
	}
	if len(host.invalidations) != 0 {
		t.Fatalf("invalidations = %v, want none", host.invalidations)
	}
}

func TestInputBridgeSuppressesAndCountsEveryLeaveDuringActiveInteraction(t *testing.T) {
	host := &recordingBrowserHost{}
	recorder := internalprofile.NewRecorder()
	start := time.Unix(100, 0)
	recorder.Start(start)
	ib := NewInputBridge(host, 1)
	ib.SetProfiler(recorder)

	ib.onMousePress(10, 20, 1, 0, 1)
	ib.onMouseMove(0, 0, 0, true) // pressed
	ib.onMouseMove(19, 20, 0, false)
	host.moves = nil
	ib.onMouseMove(0, 0, 0, true) // dragging

	if len(host.moves) != 0 {
		t.Fatalf("active leaves forwarded = %d, want 0", len(host.moves))
	}
	snap, ok := recorder.MaybeSnapshot(start.Add(time.Second), time.Second)
	if !ok || snap.SuppressedLeavesDuringDrag != 2 {
		t.Fatalf("suppressed leave snapshot = (%+v,%v), want count 2", snap, ok)
	}
}

func TestInputBridgeClaimsGestureOnceOnlyAfterThresholdCrossing(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	claims := 0
	ib.pointerTracker = NewPointerTracker(8, func() { claims++ }, nil)

	ib.onMousePress(10, 20, 1, 0, 1)
	ib.onMouseMove(18, 20, 0, false)
	ib.onMouseMove(19, 20, 0, false)
	ib.onMouseMove(30, 20, 0, false)

	if claims != 1 {
		t.Fatalf("gesture claims = %d, want exactly 1", claims)
	}
}

func TestInputBridgeGestureClaimCanReenterCancelWithoutDeadlock(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	claims := 0
	ib.pointerTracker = NewPointerTracker(8, func() {
		claims++
		ib.onMouseCancel()
	}, nil)
	ib.onMousePress(10, 20, 1, 0, 1)

	done := make(chan struct{})
	go func() {
		ib.onMouseMove(19, 20, 0, false)
		ib.onMouseMove(30, 20, 0, false)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("gesture claim deadlocked while re-entering cancel")
	}
	if claims != 1 {
		t.Fatalf("gesture claims = %d, want exactly 1", claims)
	}
}

func TestInputBridgeSuppressesIdleLeaveWithoutRealCoordinates(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)

	ib.onMouseMove(0, 0, 0, true)

	if len(host.moves) != 0 {
		t.Fatalf("leave without coordinates forwarded = %d, want 0", len(host.moves))
	}
}

func TestInputBridgeForwardsIdleLeaveAfterRealZeroCoordinateMotion(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)
	ib.onMouseMove(0, 0, 0, false)
	host.moves = nil

	ib.onMouseMove(0, 0, 0, true)

	if len(host.moves) != 1 || host.moves[0].leave != 1 || host.moves[0].event.X != 0 || host.moves[0].event.Y != 0 {
		t.Fatalf("zero-coordinate idle leave = %+v, want one leave at (0,0)", host.moves)
	}
}

func TestInputBridgeForwardsIdleLeaveWithLastRealCoordinates(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 2)
	ib.onMouseMove(12.5, 7.5, 0, false)
	host.moves = nil

	ib.onMouseMove(0, 0, 0, true)

	if len(host.moves) != 1 || host.moves[0].leave != 1 || host.moves[0].event.X != 25 || host.moves[0].event.Y != 15 {
		t.Fatalf("idle leave = %+v, want one leave at device coords (25,15)", host.moves)
	}
}

func TestInputBridgePreservesAllocationBoundaryAndOutOfAllocationMotion(t *testing.T) {
	for _, point := range [][2]float64{{640, 480}, {-1, 481}} {
		host := &recordingBrowserHost{}
		ib := NewInputBridge(host, 1)
		ib.onMouseMove(point[0], point[1], 0, false)
		ib.onMouseMove(0, 0, 0, true)

		if len(host.moves) != 2 || host.moves[0].event.X != int32(point[0]) || host.moves[0].event.Y != int32(point[1]) || host.moves[1].event != host.moves[0].event || host.moves[1].leave != 1 {
			t.Fatalf("boundary/out-of-allocation move and leave for %v = %+v", point, host.moves)
		}
	}
}

func TestInputBridgePressAndReleaseCoordinatesDriveSubsequentScroll(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 2)

	ib.onMousePress(11, 13, 1, 0, 1)
	ib.onScrollUpdate(0, 1, gdk.ScrollUnitWheelValue, true, 0)
	ib.onMouseRelease(17, 19, 1, 0, 1)
	ib.onScrollUpdate(0, 1, gdk.ScrollUnitWheelValue, true, 0)

	if len(host.wheels) != 2 {
		t.Fatalf("wheel events = %d, want 2", len(host.wheels))
	}
	if host.wheels[0].X != 22 || host.wheels[0].Y != 26 {
		t.Fatalf("scroll after press coordinates = (%d,%d), want (22,26)", host.wheels[0].X, host.wheels[0].Y)
	}
	if host.wheels[1].X != 34 || host.wheels[1].Y != 38 {
		t.Fatalf("scroll after release coordinates = (%d,%d), want (34,38)", host.wheels[1].X, host.wheels[1].Y)
	}
}

func TestInputBridgeSimpleClickRemainsUnclaimedAndForwarded(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)
	claims := 0
	ib.pointerTracker = NewPointerTracker(8, func() { claims++ }, nil)

	ib.onMousePress(10, 20, 1, 0, 1)
	ib.onMouseRelease(10, 20, 1, 0, 1)

	if claims != 0 || len(host.clicks) != 2 || host.clicks[0].mouseUp != 0 || host.clicks[1].mouseUp != 1 {
		t.Fatalf("simple click claims/clicks = %d/%+v, want unclaimed press+release", claims, host.clicks)
	}
}

func TestInputBridgeReleaseClearsOnlyReleasedButtonMask(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)
	mods := uint(gdk.ControlMaskValue | gdk.ShiftMaskValue | gdk.Button1MaskValue | gdk.Button3MaskValue)

	ib.onMouseRelease(10, 20, 1, mods, 1)

	if len(host.clicks) != 1 {
		t.Fatalf("release clicks = %d, want 1", len(host.clicks))
	}
	want := uint32(cef.EventFlagsEventflagControlDown | cef.EventFlagsEventflagShiftDown | cef.EventFlagsEventflagRightMouseButton)
	if got := host.clicks[0].event.Modifiers; got != want {
		t.Fatalf("release modifiers = %#x, want keyboard and other button modifiers %#x", got, want)
	}
}

func TestInputBridgeCancelSynthesizesTrackedMouseUpWithLastState(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 2)
	mods := uint(gdk.ControlMaskValue | gdk.ShiftMaskValue | gdk.Button1MaskValue)
	ib.onMousePress(10, 20, 1, mods, 1)
	ib.onMousePress(30, 40, 3, mods|uint(gdk.Button3MaskValue), 1)
	ib.onMouseMove(35, 45, mods|uint(gdk.Button3MaskValue), false)
	host.clicks = nil

	ib.onMouseCancel()

	if len(host.clicks) != 1 {
		t.Fatalf("synthesized clicks = %+v, want one mouseup", host.clicks)
	}
	got := host.clicks[0]
	wantModifiers := uint32(cef.EventFlagsEventflagControlDown | cef.EventFlagsEventflagShiftDown | cef.EventFlagsEventflagRightMouseButton)
	if got.event.X != 70 || got.event.Y != 90 || got.event.Modifiers != wantModifiers {
		t.Fatalf("synthesized event = %+v, want (70,90) modifiers %#x", got.event, wantModifiers)
	}
	if got.button != cef.MouseButtonTypeMbtLeft || got.mouseUp != 1 || got.count != 1 {
		t.Fatalf("synthesized click = %+v, want left mouseup count 1", got)
	}
	if host.captureLosts != 1 {
		t.Fatalf("capture lost calls = %d, want 1", host.captureLosts)
	}
	if ib.pointerTracker.Phase() != PointerIdle {
		t.Fatalf("phase = %v, want idle", ib.pointerTracker.Phase())
	}
}

func TestInputBridgeCancelDoesNotReleaseConsumedMiddleClickAndClearsConsumption(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)
	ib.SetMiddleClickHandler(func(float64, float64) bool { return true })
	ib.onMousePress(10, 20, 2, uint(gdk.Button2MaskValue), 1)

	ib.onMouseCancel()

	if len(host.clicks) != 0 {
		t.Fatalf("consumed middle cancel clicks = %+v, want none", host.clicks)
	}
	if host.captureLosts != 1 {
		t.Fatalf("capture lost calls = %d, want 1", host.captureLosts)
	}

	ib.onMouseRelease(10, 20, 2, uint(gdk.Button2MaskValue), 1)
	if len(host.clicks) != 1 || host.clicks[0].button != cef.MouseButtonTypeMbtMiddle || host.clicks[0].mouseUp != 1 {
		t.Fatalf("release after consumed cancel = %+v, want one forwarded middle release", host.clicks)
	}
}

func TestInputBridgeCancelRecordsOnlySynthesizedUnmatchedRelease(t *testing.T) {
	host := &recordingBrowserHost{}
	recorder := internalprofile.NewRecorder()
	start := time.Unix(100, 0)
	recorder.Start(start)
	ib := NewInputBridge(host, 1)
	ib.SetProfiler(recorder)

	ib.onMouseCancel() // idle
	ib.onMousePress(10, 20, 1, uint(gdk.Button1MaskValue), 1)
	ib.ArmDnd()
	ib.onMouseCancel() // native DnD owns completion
	ib.DisarmDnd()
	ib.onMousePress(30, 40, 1, uint(gdk.Button1MaskValue), 1)
	ib.onMouseCancel() // bridge synthesizes the missing release

	snap, ok := recorder.MaybeSnapshot(start.Add(time.Second), time.Second)
	if !ok || snap.PressesWithoutMatchedRelease != 1 {
		t.Fatalf("unmatched release snapshot = (%+v,%v), want count 1", snap, ok)
	}
}

func TestInputBridgeCancelWithNilHostReturnsTrackerToIdle(t *testing.T) {
	ib := NewInputBridge(nil, 1)
	ib.onMousePress(10, 20, 1, uint(gdk.Button1MaskValue), 1)

	ib.onMouseCancel()

	if ib.pointerTracker.Phase() != PointerIdle {
		t.Fatalf("phase = %v, want idle", ib.pointerTracker.Phase())
	}
}

func TestInputBridgeIdleCancelDoesNotSynthesizeEvents(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)

	ib.onMouseCancel()

	if len(host.clicks) != 0 || host.captureLosts != 0 {
		t.Fatalf("idle cancel clicks/capture lost = %d/%d, want 0/0", len(host.clicks), host.captureLosts)
	}
}

func TestInputBridgeNativeDndCompletionWithoutCancelAllowsFreshInteraction(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)
	ib.onMousePress(10, 20, 1, uint(gdk.Button1MaskValue), 1)
	ib.onMouseMove(19, 20, uint(gdk.Button1MaskValue), false)
	ib.ArmDnd()
	host.clicks = nil

	ib.DisarmDnd()

	if ib.pointerTracker.Phase() != PointerIdle {
		t.Fatalf("phase after native DnD completion = %v, want idle", ib.pointerTracker.Phase())
	}
	if len(host.clicks) != 0 || host.captureLosts != 0 {
		t.Fatalf("DnD completion clicks/capture lost = %d/%d, want 0/0", len(host.clicks), host.captureLosts)
	}

	ib.onMousePress(30, 40, 1, uint(gdk.Button1MaskValue), 1)
	if ib.pointerTracker.Phase() != PointerPressed {
		t.Fatalf("phase after fresh slider press = %v, want pressed", ib.pointerTracker.Phase())
	}
	ib.onMouseRelease(35, 40, 1, uint(gdk.Button1MaskValue), 1)

	if ib.pointerTracker.Phase() != PointerIdle {
		t.Fatalf("phase after fresh slider release = %v, want idle", ib.pointerTracker.Phase())
	}
	if len(host.clicks) != 2 || host.clicks[0].mouseUp != 0 || host.clicks[1].mouseUp != 1 {
		t.Fatalf("fresh slider clicks = %+v, want press and release", host.clicks)
	}
	if host.captureLosts != 0 {
		t.Fatalf("capture lost calls = %d, want 0", host.captureLosts)
	}
}

func TestInputBridgeDndSuspendsCancelAndRestoresRecoveryAfterDisarm(t *testing.T) {
	host := &recordingBrowserHost{}
	ib := NewInputBridge(host, 1)
	ib.onMousePress(10, 20, 1, uint(gdk.Button1MaskValue), 1)
	host.clicks = nil
	ib.ArmDnd()

	ib.onMouseCancel()

	if len(host.clicks) != 0 || host.captureLosts != 0 || ib.pointerTracker.Phase() != PointerPressed {
		t.Fatalf("armed cancel clicks/capture/phase = %d/%d/%v, want 0/0/pressed", len(host.clicks), host.captureLosts, ib.pointerTracker.Phase())
	}

	ib.DisarmDnd()
	if ib.pointerTracker.Phase() != PointerIdle {
		t.Fatalf("phase after DnD cancel completion = %v, want idle", ib.pointerTracker.Phase())
	}

	ib.onMousePress(30, 40, 1, uint(gdk.Button1MaskValue), 1)
	host.clicks = nil
	ib.onMouseCancel()
	if len(host.clicks) != 1 || host.captureLosts != 1 || ib.pointerTracker.Phase() != PointerIdle {
		t.Fatalf("restored cancel clicks/capture/phase = %d/%d/%v, want 1/1/idle", len(host.clicks), host.captureLosts, ib.pointerTracker.Phase())
	}
}

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

func TestInjectClipboardTextUsesCEFFocusedFrame(t *testing.T) {
	focused := &recordingFrame{}
	main := &recordingFrame{}
	host := &recordingBrowserHost{browser: &recordingBrowser{focusedFrame: focused, mainFrame: main}}
	ib := NewInputBridge(host, 1)

	ib.injectClipboardText("secret")

	if len(focused.scripts) != 1 {
		t.Fatalf("focused frame scripts = %d, want 1", len(focused.scripts))
	}
	if len(main.scripts) != 0 {
		t.Fatalf("main frame scripts = %d, want 0", len(main.scripts))
	}
}

func TestInjectClipboardTextFallsBackToMainFrame(t *testing.T) {
	main := &recordingFrame{}
	host := &recordingBrowserHost{browser: &recordingBrowser{mainFrame: main}}
	ib := NewInputBridge(host, 1)

	ib.injectClipboardText("secret")

	if len(main.scripts) != 1 {
		t.Fatalf("main frame scripts = %d, want 1", len(main.scripts))
	}
}

func TestPasteJavaScriptUsesChromiumEditingThenControlledInputFallback(t *testing.T) {
	script := pasteJavaScript("pa'ss</script>\n")
	for _, want := range []string{
		"document.execCommand('insertText',false,text)",
		"Object.getOwnPropertyDescriptor(proto,'value')",
		"setter.call(active,next)",
		"new InputEvent('input'",
		`"pa'ss<\/script>\n"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("paste script does not contain %q:\n%s", want, script)
		}
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
