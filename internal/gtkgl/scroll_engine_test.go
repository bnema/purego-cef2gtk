package gtkgl

import (
	"math"
	"sync"
	"testing"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gtk"
)

type gateSubmission struct {
	evt    cef.MouseEvent
	dx, dy int32
}

type gateRecorder struct {
	subs []gateSubmission
}

func (r *gateRecorder) send(_ cef.BrowserHost, evt *cef.MouseEvent, dx, dy int32) {
	r.subs = append(r.subs, gateSubmission{evt: *evt, dx: dx, dy: dy})
}

func (r *gateRecorder) total() (int64, int64) {
	var x, y int64
	for _, s := range r.subs {
		x += int64(s.dx)
		y += int64(s.dy)
	}
	return x, y
}

func newEngineController(rec *gateRecorder) (*scrollController, *scrollWheelCapture) {
	c := newScrollController()
	host := &scrollWheelCapture{}
	if rec != nil {
		c.sender = rec.send
	}
	return c, host
}

func stepUntilDone(c *scrollController, from, dt float64) (float64, int) {
	t := from
	steps := 0
	for i := 0; i < 10000; i++ {
		t += dt
		steps++
		if !c.step(t) {
			return t, steps
		}
	}
	return t, steps
}

func armTouchRelease(t *testing.T, c *scrollController, host cef.BrowserHost, now float64, vx, vy float64) {
	t.Helper()
	c.beginTouch(10, 20, 1, 0, host)
	for i := 0; i < 3; i++ {
		if !c.updateTouch(gdk.ScrollUnitSurfaceValue, true, false, 0, 10, 20) {
			t.Fatal("touch update not accepted")
		}
	}
	c.endTouch()
	if !c.releaseFromDecelerate(now, 10, 20, 1, 0, host, vx, vy, ScrollOptions{}) {
		t.Fatal("release not armed")
	}
}

func TestEngineTouchpadReleaseDecaysFinite(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	armTouchRelease(t, c, host, 1.0, 800, -400)

	_, steps := stepUntilDone(c, 1.0, 1.0/60)
	if steps > 200 {
		t.Fatalf("release steps = %d, want finite decay under 200", steps)
	}
	if len(rec.subs) == 0 {
		t.Fatal("release submitted nothing")
	}
	tx, ty := rec.total()
	// Output velocity is 800*2.5=2000 u/s and 400*2.5=1000 u/s; totals near
	// v0*tau per axis minus integer quantization and the sub-threshold tail.
	if math.Abs(float64(tx)-360) > 4 || math.Abs(float64(ty)-180) > 4 {
		t.Fatalf("release total = (%d,%d), want near (360,180)", tx, ty)
	}
	for _, s := range rec.subs {
		if s.evt.Modifiers&uint32(cef.EventFlagsEventflagPrecisionScrollingDelta) == 0 {
			t.Fatalf("synthetic modifiers = %#x, want precision bit", s.evt.Modifiers)
		}
	}
	if c.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want cleared after release", c.session.kind)
	}
}

func TestEngineReleaseUsesUnitsPerSecond(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	armTouchRelease(t, c, host, 1.0, 1000, 0)

	stepUntilDone(c, 1.0, 1.0/60)
	tx, _ := rec.total()
	// 1000 u/s * 2.5 * 0.18 s = 450 output units. A mistaken extra x1000
	// (pixels/ms confusion) would yield 450000.
	if tx <= 0 || tx > 2000 {
		t.Fatalf("release total = %d, want near 450 (units/s, not units/ms)", tx)
	}
}

func TestEngineReleaseMatchesAcrossFrameRates(t *testing.T) {
	run := func(dt float64) int64 {
		rec := &gateRecorder{}
		c, host := newEngineController(rec)
		armTouchRelease(t, c, host, 1.0, 800, -400)
		stepUntilDone(c, 1.0, dt)
		tx, _ := rec.total()
		return tx
	}
	x60 := run(1.0 / 60)
	x144 := run(1.0 / 144)
	if math.Abs(float64(x60-x144)) > 3 {
		t.Fatalf("frame-rate divergence: 60Hz=%d 144Hz=%d", x60, x144)
	}
}

func TestEngineHoldOnlyNeverReleases(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.beginTouch(10, 20, 1, 0, host)
	c.endTouch()
	if c.releaseFromDecelerate(1.0, 10, 20, 1, 0, host, 500, 0, ScrollOptions{}) {
		t.Fatal("hold-only gesture armed release")
	}
	if c.step(1.5) {
		t.Fatal("idle controller keeps ticking")
	}
	if len(rec.subs) != 0 {
		t.Fatalf("submissions = %d, want 0", len(rec.subs))
	}
}

func TestEngineConsumedGestureIneligible(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.beginTouch(10, 20, 1, 0, host)
	c.updateTouch(gdk.ScrollUnitSurfaceValue, true, true, 0, 10, 20)
	c.endTouch()
	if c.releaseFromDecelerate(1.0, 10, 20, 1, 0, host, 900, 0, ScrollOptions{}) {
		t.Fatal("consumed gesture armed release")
	}
}

func TestEngineInvalidVelocityCancels(t *testing.T) {
	for _, v := range [][2]float64{{math.NaN(), 0}, {0, math.Inf(1)}, {1, 1}} {
		rec := &gateRecorder{}
		c, host := newEngineController(rec)
		c.beginTouch(10, 20, 1, 0, host)
		c.updateTouch(gdk.ScrollUnitSurfaceValue, true, false, 0, 10, 20)
		c.endTouch()
		if c.releaseFromDecelerate(1.0, 10, 20, 1, 0, host, v[0], v[1], ScrollOptions{}) {
			t.Fatalf("velocity %v armed release", v)
		}
		if len(rec.subs) != 0 {
			t.Fatalf("velocity %v submitted %d events", v, len(rec.subs))
		}
	}
}

func TestEngineImmediateRetouchCancelsRelease(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	armTouchRelease(t, c, host, 1.0, 800, 0)
	c.step(1.05)
	first := len(rec.subs)
	if first == 0 {
		t.Fatal("release emitted nothing before retouch")
	}
	c.beginTouch(12, 22, 1, 0, host)
	c.step(1.2)
	if len(rec.subs) != first {
		t.Fatalf("submissions after retouch = %d, want %d (old release dead)", len(rec.subs), first)
	}
}

func TestEngineLateDecelerateAfterInvalidateIgnored(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.beginTouch(10, 20, 1, 0, host)
	c.updateTouch(gdk.ScrollUnitSurfaceValue, true, false, 0, 10, 20)
	c.endTouch()
	c.invalidate()
	if c.releaseFromDecelerate(1.0, 10, 20, 1, 0, host, 900, 0, ScrollOptions{}) {
		t.Fatal("invalidated gesture armed release from late decelerate")
	}
}

func TestEngineWheelConservesUninterruptedBurst(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	base := 100.0
	for i := 0; i < 2; i++ {
		c.impulseWheel(base+float64(i)*0.016, 10, 20, 1, 0, host, 20, -10)
	}
	stepUntilDone(c, base+0.016, 1.0/60)
	tx, ty := rec.total()
	// 40/-20 units in, conserved within integer quantization plus the
	// retained sub-unit session remainder.
	if math.Abs(float64(tx)-40) > 3 || math.Abs(float64(ty)+20) > 3 {
		t.Fatalf("burst total = (%d,%d), want near (40,-20)", tx, ty)
	}
	// The settled burst retains its fractional remainder until the idle
	// deadline clears it without further output.
	if c.session.kind != scrollSessionWheel || !c.session.burstActive {
		t.Fatalf("settled burst not retained: %+v", c.session)
	}
	idled := base + 0.016 + scrollWheelIdleTimeout + 0.05
	if c.step(idled) {
		t.Fatal("idle burst keeps ticking")
	}
	if c.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want cleared past idle deadline", c.session.kind)
	}
}

func TestEngineWheelReversalNetsPending(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0)
	c.impulseWheel(100.016, 10, 20, 1, 0, host, -30, 0)
	stepUntilDone(c, 100.016, 1.0/60)
	tx, _ := rec.total()
	if math.Abs(float64(tx)) > 3 {
		t.Fatalf("reversal total = %d, want near 0", tx)
	}
}

func TestEngineWheelNotchConservesFully(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	// One wheel notch carries 240 output units. It settles right around
	// the idle deadline, so the run may end through settling or through
	// one explicit overload completion; either way displacement is
	// conserved within a couple of units.
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0, 240)
	stepUntilDone(c, 100.0, 1.0/60)
	if c.overloadCompletions > 1 {
		t.Fatalf("overload completions = %d, want at most 1", c.overloadCompletions)
	}
	_, ty := rec.total()
	if math.Abs(float64(ty)-240) > 5 {
		t.Fatalf("notch total = %d, want near 240", ty)
	}
	if c.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want cleared", c.session.kind)
	}
}

func TestEngineWheelOverloadCompletionIsExplicit(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	// A flood impulse cannot settle within the idle deadline: at 250 ms
	// after the last impulse a multi-unit remainder is discarded as an
	// explicit overload completion, never conserved motion.
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0, 2000)
	stepUntilDone(c, 100.0, 1.0/60)
	if c.overloadCompletions != 1 {
		t.Fatalf("overload completions = %d, want 1 recorded exception", c.overloadCompletions)
	}
	_, ty := rec.total()
	if ty < 1980 || ty > 2000 {
		t.Fatalf("flood total = %d, want [1980,2000] (remainder explicitly discarded)", ty)
	}
}

func TestEngineWheelEnvelopeOverloadCancels(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, scrollOverloadEnvelope*4, 0)
	if len(rec.subs) != 0 {
		t.Fatalf("overload submitted %d events", len(rec.subs))
	}
	if c.session.burstActive {
		t.Fatal("burst survives envelope overload")
	}
}

func TestEngineWheelNonFiniteRejected(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, math.NaN(), 0)
	if c.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want none for NaN impulse", c.session.kind)
	}
	if len(rec.subs) != 0 {
		t.Fatalf("NaN impulse submitted %d events", len(rec.subs))
	}
}

func TestEngineWheelStallCancelsWithoutCatchUp(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0)
	c.step(100.05)
	before := len(rec.subs)
	// A 150 ms frame gap exceeds the stall threshold but not the idle
	// deadline: motion cancels plainly, with no overload recorded.
	if c.step(100.2) {
		t.Fatal("stalled burst keeps ticking")
	}
	if len(rec.subs) != before {
		t.Fatalf("stall emitted %d catch-up events", len(rec.subs)-before)
	}
	if c.overloadCompletions != 0 {
		t.Fatal("stall recorded as overload")
	}
}

func TestEngineWheelSettledRemainderResumes(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 5, 0)
	stepUntilDone(c, 100.0, 1.0/60)
	if c.session.kind != scrollSessionWheel || !c.session.burstActive {
		t.Fatalf("settled burst not retained: %+v", c.session)
	}
	// A same-anchor impulse inside the idle window resumes the burst and
	// reuses the retained fractional remainder.
	c.impulseWheel(100.15, 10, 20, 1, 0, host, 5, 0)
	stepUntilDone(c, 100.15, 1.0/60)
	tx, _ := rec.total()
	if math.Abs(float64(tx)-10) > 2 {
		t.Fatalf("resumed total = %d, want near 10", tx)
	}
}

func TestEngineWheelSparseOutputLosesLatch(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0.5, 0)
	if c.step(100.05) {
		t.Fatal("sub-unit burst keeps ticking")
	}
	if len(rec.subs) != 0 {
		t.Fatalf("sub-unit burst submitted %d events", len(rec.subs))
	}
	if c.step(100.4) {
		t.Fatal("idle burst keeps ticking")
	}
	if c.session.kind != scrollSessionNone {
		t.Fatalf("session kind = %v, want cleared past idle deadline", c.session.kind)
	}
}

func TestEngineModifierInterruption(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0)
	c.noteModifiers(uint(gdk.ShiftMaskValue))
	if c.session.kind != scrollSessionNone {
		t.Fatalf("burst survives modifier change: %+v", c.session)
	}
	if c.step(100.05) {
		t.Fatal("interrupted burst keeps ticking")
	}
}

func TestEnginePointerAnchorChangeEndsBurst(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0)
	c.notePointer(10, 20)
	if c.session.kind != scrollSessionWheel || !c.session.burstActive {
		t.Fatal("burst died on identical anchor")
	}
	c.notePointer(40, 20)
	if c.session.burstActive {
		t.Fatal("burst survives pointer A-to-B movement")
	}
	if c.session.pendingX != 0 {
		t.Fatalf("pending = %v, want discarded", c.session.pendingX)
	}
}

func TestEngineSubmitGateRejectsStaleEpoch(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.beginTouch(10, 20, 1, 0, host)
	epoch := c.epoch.Load()
	c.invalidate()
	evt := cef.MouseEvent{}
	if c.submitGated(epoch, host, &evt, 10, 10, 200.0) {
		t.Fatal("stale epoch submitted through gate")
	}
	if c.submitPhysical(epoch, host, &evt, 10, 10, 200.0) {
		t.Fatal("stale epoch submitted through physical path")
	}
	if len(rec.subs) != 0 {
		t.Fatalf("stale submissions = %d, want 0", len(rec.subs))
	}
	// The live epoch still submits.
	evt2 := cef.MouseEvent{}
	c.ensureTouchSession(10, 20, 1, 0, host)
	if !c.submitPhysical(c.epoch.Load(), host, &evt2, 5, 5, 200.0) {
		t.Fatal("live epoch rejected")
	}
	if len(rec.subs) != 1 {
		t.Fatalf("submissions = %d, want 1", len(rec.subs))
	}
}

func TestEngineSubmitGateRejectsHostMismatch(t *testing.T) {
	rec := &gateRecorder{}
	c, hostA := newEngineController(rec)
	hostB := &scrollWheelCapture{}
	c.beginTouch(10, 20, 1, 0, hostA)
	evt := cef.MouseEvent{}
	if c.submitGated(c.epoch.Load(), hostB, &evt, 10, 10, 200.0) {
		t.Fatal("replaced host submitted through gate")
	}
	if len(rec.subs) != 0 {
		t.Fatalf("submissions = %d, want 0", len(rec.subs))
	}
}

func TestEngineReentrantInvalidateDuringSubmit(t *testing.T) {
	c, host := newEngineController(nil)
	calls := 0
	c.sender = func(h cef.BrowserHost, evt *cef.MouseEvent, dx, dy int32) {
		calls++
		c.invalidate()
	}
	armTouchRelease(t, c, host, 1.0, 800, 0)
	stepUntilDone(c, 1.0, 1.0/60)
	// The first submission invalidated the session; exactly one gated
	// submission escapes and the rest are rejected without deadlock.
	if calls != 1 {
		t.Fatalf("sender calls = %d, want 1", calls)
	}
}

func TestEngineBeginPreservesLiveWheelBurst(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0)
	epochBefore := c.epoch.Load()
	returned := c.beginTouch(10, 20, 1, 0, host)
	if returned != epochBefore {
		t.Fatalf("begin epoch = %d, want untouched %d", returned, epochBefore)
	}
	if c.session.kind != scrollSessionWheel || !c.session.burstActive {
		t.Fatalf("begin discarded live burst: %+v", c.session)
	}
	if c.session.pendingX != 30 {
		t.Fatalf("burst pending = %v, want 30", c.session.pendingX)
	}
	// A wheel update after the begin joins the preserved burst.
	c.impulseWheel(100.05, 10, 20, 1, 0, host, 30, 0)
	if c.session.pendingX <= 30 {
		t.Fatalf("preserved burst did not accumulate: %v", c.session.pendingX)
	}
}

func TestEngineBeginReplacesStaleBurst(t *testing.T) {
	c, host := newEngineController(nil)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0)
	c.invalidate()
	c.beginTouch(10, 20, 1, 0, host)
	if c.session.kind != scrollSessionTouchpad || !c.session.touchActive {
		t.Fatalf("begin did not start touch session: %+v", c.session)
	}
}

func TestEngineOldCleanupKeepsNewSession(t *testing.T) {
	c, host := newEngineController(nil)
	c.beginTouch(10, 20, 1, 0, host)
	stale := c.epoch.Load()
	c.invalidate()
	c.beginTouch(10, 20, 1, 0, host)
	if c.cleanupEpoch(stale) {
		t.Fatal("stale cleanup reported success")
	}
	if c.session.kind != scrollSessionTouchpad || !c.session.touchActive {
		t.Fatalf("new session clobbered: %+v", c.session)
	}
	if !c.cleanupEpoch(c.epoch.Load()) {
		t.Fatal("current cleanup reported failure")
	}
	if c.session.kind != scrollSessionNone {
		t.Fatalf("session survives current cleanup: %+v", c.session)
	}
}

func TestEngineConcurrentInvalidateVsSubmit(t *testing.T) {
	c, host := newEngineController(nil)
	var mu sync.Mutex
	calls := 0
	c.sender = func(_ cef.BrowserHost, _ *cef.MouseEvent, _, _ int32) {
		mu.Lock()
		calls++
		mu.Unlock()
	}
	c.beginTouch(10, 20, 1, 0, host)
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { recover() }()
			for j := 0; j < 50; j++ {
				evt := cef.MouseEvent{}
				c.submitGated(c.epoch.Load(), host, &evt, 1, 1, 300.0)
				c.invalidate()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	// After a final invalidation settles, the gate rejects everything.
	c.invalidate()
	stale := c.epoch.Load()
	c.invalidate()
	evt := cef.MouseEvent{}
	if c.submitGated(stale, host, &evt, 1, 1, 300.0) {
		t.Fatal("post-join stale submission accepted")
	}
}

type stubTickBackend struct {
	next    uint
	regIDs  []uint
	removed []uint
	unrefs  int
}

func (s *stubTickBackend) backend() *scrollTickBackend {
	return &scrollTickBackend{
		registrar: func(_ *gtk.TickCallback) uint {
			s.next++
			s.regIDs = append(s.regIDs, s.next)
			return s.next
		},
		remover: func(id uint) {
			s.removed = append(s.removed, id)
		},
		unrefer: func(_ *gtk.TickCallback) {
			s.unrefs++
		},
	}
}

func TestEngineTickReplacementAndTeardown(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	stub := &stubTickBackend{}
	c.tickBackend = stub.backend()
	clock := 1.0
	c.now = func() float64 { return clock }
	armTouchRelease(t, c, host, 1.0, 800, 0)
	if len(stub.regIDs) != 1 {
		t.Fatalf("registrations = %v, want one tick", stub.regIDs)
	}
	firstID := stub.regIDs[0]
	// A stale trampoline generation cannot clear the live tick ID.
	clock = 1.01
	c.tickFire(999)
	if c.tickState.id != firstID {
		t.Fatalf("tick id = %d, want %d after stale trampoline", c.tickState.id, firstID)
	}
	stepUntilDone(c, 1.0, 1.0/60)
	if stub.unrefs != 0 {
		t.Fatalf("unrefs = %d during stepping, want 0 (teardown owns unref)", stub.unrefs)
	}
	// A second release retains the callback slot and registers a new ID;
	// the old registration is removed exactly once.
	armTouchRelease(t, c, host, 5.0, 800, 0)
	if len(stub.regIDs) != 2 {
		t.Fatalf("registrations = %v, want replacement tick", stub.regIDs)
	}
	removed := 0
	for _, id := range stub.removed {
		if id == firstID {
			removed++
		}
	}
	if removed != 1 {
		t.Fatalf("old tick removals = %d, want 1", removed)
	}
	c.teardownTick()
	if stub.unrefs != 1 {
		t.Fatalf("unrefs = %d, want exactly-once teardown", stub.unrefs)
	}
	if c.tickBackend != nil {
		t.Fatal("backend survives teardown")
	}
}
