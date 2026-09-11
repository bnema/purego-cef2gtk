package gtkgl

import (
	"fmt"
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

func stepUntilDone(t *testing.T, c *scrollController, from, dt float64) (float64, int) {
	t.Helper()
	now := from
	steps := 0
	for i := 0; i < 10000; i++ {
		now += dt
		steps++
		if !c.step(now) {
			return now, steps
		}
	}
	t.Fatalf("scroll animation did not terminate within 10000 frames from %.3f", from)
	return now, steps
}

func armTouchRelease(t *testing.T, c *scrollController, host cef.BrowserHost, now float64, vx, vy float64) {
	t.Helper()
	c.beginTouch(10, 20, 1, 0, host)
	for i := 0; i < 3; i++ {
		if !c.updateTouch(gdk.ScrollUnitSurfaceValue, true, false, 0, 10, 20, c.epoch.Load()) {
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

	_, steps := stepUntilDone(t, c, 1.0, 1.0/60)
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

	stepUntilDone(t, c, 1.0, 1.0/60)
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
		stepUntilDone(t, c, 1.0, dt)
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
	c.updateTouch(gdk.ScrollUnitSurfaceValue, true, true, 0, 10, 20, c.epoch.Load())
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
		c.updateTouch(gdk.ScrollUnitSurfaceValue, true, false, 0, 10, 20, c.epoch.Load())
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
	c.updateTouch(gdk.ScrollUnitSurfaceValue, true, false, 0, 10, 20, c.epoch.Load())
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
		c.impulseWheel(base+float64(i)*0.016, 10, 20, 1, 0, host, 20, -10, c.epoch.Load())
	}
	stepUntilDone(t, c, base+0.016, 1.0/60)
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
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0, c.epoch.Load())
	c.impulseWheel(100.016, 10, 20, 1, 0, host, -30, 0, c.epoch.Load())
	stepUntilDone(t, c, 100.016, 1.0/60)
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
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0, 240, c.epoch.Load())
	stepUntilDone(t, c, 100.0, 1.0/60)
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
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0, 2000, c.epoch.Load())
	stepUntilDone(t, c, 100.0, 1.0/60)
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
	c.impulseWheel(100.0, 10, 20, 1, 0, host, scrollOverloadEnvelope*4, 0, c.epoch.Load())
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
	c.impulseWheel(100.0, 10, 20, 1, 0, host, math.NaN(), 0, c.epoch.Load())
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
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0, c.epoch.Load())
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
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 5, 0, c.epoch.Load())
	stepUntilDone(t, c, 100.0, 1.0/60)
	if c.session.kind != scrollSessionWheel || !c.session.burstActive {
		t.Fatalf("settled burst not retained: %+v", c.session)
	}
	// A same-position impulse inside the idle window resumes the burst and
	// reuses the retained fractional remainder.
	c.impulseWheel(100.15, 10, 20, 1, 0, host, 5, 0, c.epoch.Load())
	stepUntilDone(t, c, 100.15, 1.0/60)
	tx, _ := rec.total()
	if math.Abs(float64(tx)-10) > 2 {
		t.Fatalf("resumed total = %d, want near 10", tx)
	}
}

func TestEngineWheelSparseOutputLosesLatch(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0.5, 0, c.epoch.Load())
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
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0, c.epoch.Load())
	c.noteModifiers(uint(gdk.ShiftMaskValue))
	if c.session.kind != scrollSessionNone {
		t.Fatalf("burst survives modifier change: %+v", c.session)
	}
	if c.step(100.05) {
		t.Fatal("interrupted burst keeps ticking")
	}
}

func TestEngineFiveNotchBurstSurvivesPointerDrift(t *testing.T) {
	// Replay of scroll-trace.log 14:28 (5x dy=1 -> 5x -240 with pointer
	// drift 946,1472 -> 944,1484): the old anchor kill discarded -737
	// of -1200 after the last tick. Displaced impulses must join the
	// live burst instead of restarting or killing it.
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	t0 := 13.866
	pts := [][2]float64{{946.6, 1472.6}, {946.0, 1476.0}, {945.2, 1479.0}, {944.6, 1482.0}, {944.2, 1484.6}}
	for i, pt := range pts {
		now := t0 + float64(i)*0.004
		c.impulseWheel(now, pt[0], pt[1], 1, 0, host, 0, -240, c.epoch.Load())
	}
	if !c.session.burstActive {
		t.Fatalf("drift killed 5-notch burst: %+v", c.session)
	}
	stepUntilDone(t, c, t0+0.02, 1.0/165)
	_, ty := rec.total()
	// The tail keeps emitting after the last notch; only the sub-unit
	// remainder and the documented idle-deadline overload cut (<1% here)
	// may remain. Before the fix only -463 of -1200 survived.
	if ty > -1100 {
		t.Fatalf("5-notch total = %d, want at most -1100 of -1200", ty)
	}
}

func TestEngineWheelEmitRejectsRacingInvalidation(t *testing.T) {
	// An off-thread invalidation landing after the step's epoch check
	// but before delivery must reject the wheel submission: the flush
	// path validates the session epoch, never the reloaded current.
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0, -240, c.epoch.Load())
	stale := c.invalidate()
	c.mu.Lock()
	s := &c.session
	if s.epoch != stale || !s.burstActive {
		c.mu.Unlock()
		t.Fatalf("session not intact after invalidate: %+v", s)
	}
	s.queuedX = 12
	c.flushWheelQueueLocked(s, 100.05)
	c.mu.Unlock()
	if len(rec.subs) != 0 {
		t.Fatalf("racing invalidation submitted %d events", len(rec.subs))
	}
}

func TestEngineStaleEpochImpulseRejected(t *testing.T) {
	// An impulse carrying a pre-invalidation epoch is dropped instead of
	// opening an adopted session under the new epoch.
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	live := c.epoch.Load()
	c.invalidate()
	if c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0, live) {
		t.Fatal("stale-epoch impulse accepted")
	}
	if c.session.kind != scrollSessionNone || c.session.burstActive {
		t.Fatalf("stale impulse opened session: %+v", c.session)
	}
	if len(rec.subs) != 0 {
		t.Fatalf("stale impulse submitted %d events", len(rec.subs))
	}
}

func TestEngineStaleEpochTouchStartRejected(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	live := c.epoch.Load()
	c.invalidate()
	c.ensureTouchSession(10, 20, 1, 0, host, live)
	if c.session.kind != scrollSessionNone {
		t.Fatalf("stale touch start opened session: %+v", c.session)
	}
	if c.updateTouch(gdk.ScrollUnitSurfaceValue, true, false, 0, 10, 20, live) {
		t.Fatal("stale-epoch touch update accepted")
	}
}

func TestControllerEpochBasesUniqueAcrossBridges(t *testing.T) {
	// Cleanup tokens must not collide across bridge instances: a stale
	// token from a detached bridge cannot match a live session after
	// reattach.
	a := newScrollController()
	b := newScrollController()
	ea, eb := a.epoch.Load(), b.epoch.Load()
	if eb <= ea || eb-ea < scrollEpochBlock {
		t.Fatalf("epoch bases = (%d,%d), want distinct blocks", ea, eb)
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
	c.ensureTouchSession(10, 20, 1, 0, host, c.epoch.Load())
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
	stepUntilDone(t, c, 1.0, 1.0/60)
	// The first submission invalidated the session; exactly one gated
	// submission escapes and the rest are rejected without deadlock.
	if calls != 1 {
		t.Fatalf("sender calls = %d, want 1", calls)
	}
}

func TestEngineBeginPreservesLiveWheelBurst(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0, c.epoch.Load())
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
	c.impulseWheel(100.05, 10, 20, 1, 0, host, 30, 0, c.epoch.Load())
	if c.session.pendingX <= 30 {
		t.Fatalf("preserved burst did not accumulate: %v", c.session.pendingX)
	}
}

func TestEngineBeginReplacesStaleBurst(t *testing.T) {
	c, host := newEngineController(nil)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0, c.epoch.Load())
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

func TestEngineImpulseAcrossPointerPositionsJoinsBurst(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0, c.epoch.Load())
	// Jittered and teleported impulses all join the live burst instead
	// of restarting it; delivery stays at the frozen origin.
	c.impulseWheel(100.05, 12, 21, 1, 0, host, 30, 0, c.epoch.Load())
	c.impulseWheel(100.1, 40, 20, 1, 0, host, 30, 0, c.epoch.Load())
	if c.session.pendingX <= 30 {
		t.Fatalf("displaced impulses did not join burst: %v", c.session.pendingX)
	}
	if c.session.x != 10 || c.session.y != 20 {
		t.Fatalf("origin moved to (%v,%v), want frozen (10,20)", c.session.x, c.session.y)
	}
}

func TestEngineSyntheticDeliveryUsesFrozenOrigin(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 30, 0, c.epoch.Load())
	// A far displaced impulse joins the burst; delivery stays at the
	// frozen origin.
	c.impulseWheel(100.05, 400, 900, 1, 0, host, 30, 0, c.epoch.Load())
	c.step(100.1)
	if len(rec.subs) == 0 {
		t.Fatal("burst submitted nothing")
	}
	for _, s := range rec.subs {
		if s.evt.X != 10 || s.evt.Y != 20 {
			t.Fatalf("synthetic coords = (%d,%d), want frozen anchor (10,20)", s.evt.X, s.evt.Y)
		}
	}
}

func TestEngineMirroredSignReplay(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, -30, 15, c.epoch.Load())
	stepUntilDone(t, c, 100.0, 1.0/60)
	tx, ty := rec.total()
	if tx >= 0 || ty <= 0 {
		t.Fatalf("mirrored total = (%d,%d), want (-,+)", tx, ty)
	}
	if !c.session.burstActive {
		t.Fatal("settled burst not retained")
	}
}

func TestEngineStaleSessionStepSubmitsNothing(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	armTouchRelease(t, c, host, 1.0, 800, 0)
	c.invalidate()
	c.step(1.05)
	if len(rec.subs) != 0 {
		t.Fatalf("invalidated release submitted %d events", len(rec.subs))
	}
	if c.session.kind != scrollSessionNone {
		t.Fatalf("stale session survives step: %+v", c.session)
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
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("worker panicked: %v", r)
				}
				done <- struct{}{}
			}()
			for j := 0; j < 50; j++ {
				evt := cef.MouseEvent{}
				c.submitGated(c.epoch.Load(), host, &evt, 1, 1, 300.0)
				c.invalidate()
			}
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
	stepUntilDone(t, c, 1.0, 1.0/60)
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

// wheelTickRecorder attributes every sender call to the frame tick that was
// current when it happened, so a replay can assert per-tick delivery counts.
type wheelTickRecorder struct {
	subs  []gateSubmission
	seens []int
	tick  int
}

func (r *wheelTickRecorder) send(_ cef.BrowserHost, evt *cef.MouseEvent, dx, dy int32) {
	r.subs = append(r.subs, gateSubmission{evt: *evt, dx: dx, dy: dy})
	r.seens = append(r.seens, r.tick)
}

func (r *wheelTickRecorder) total() (int64, int64) {
	var x, y int64
	for _, sub := range r.subs {
		x += int64(sub.dx)
		y += int64(sub.dy)
	}
	return x, y
}

func (r *wheelTickRecorder) maxCallsPerTick() int {
	perTick := map[int]int{}
	max := 0
	for _, tick := range r.seens {
		perTick[tick]++
		if perTick[tick] > max {
			max = perTick[tick]
		}
	}
	return max
}

// Dense replay parameters: impulses arrive well above every tested frame rate,
// so several impulses fall inside one frame tick.
const denseImpulseRate = 240.0

// denseReplay describes a wheel replay: impulses at denseImpulseRate and frame
// ticks at tickRate, interleaved in timestamp order.
type denseReplay struct {
	tickRate int
	seconds  float64
	dx, dy   float64
	// alternateEvery flips the sign of the impulse every n impulses, emulating a
	// direction change at a frame boundary (n = impulses per tick) or inside one
	// frame (n = 1). Zero keeps the direction constant.
	alternateEvery int
}

// run drives the replay and returns the total input displacement it fed in.
// After the impulses it drains ticks for less than the wheel idle deadline, so
// no documented overload completion is involved.
func (cfg denseReplay) run(c *scrollController, host cef.BrowserHost, rec *wheelTickRecorder) (inputX, inputY float64) {
	const start = 100.0
	impulseStep := 1.0 / denseImpulseRate
	tickStep := 1.0 / float64(cfg.tickRate)
	nextTick := start + tickStep
	for i := 0; i <= int(cfg.seconds*denseImpulseRate); i++ {
		now := start + float64(i)*impulseStep
		dx, dy := cfg.dx, cfg.dy
		if cfg.alternateEvery > 0 && (i/cfg.alternateEvery)%2 == 1 {
			dx, dy = -dx, -dy
		}
		c.impulseWheel(now, 10, 20, 1, 0, host, dx, dy, c.epoch.Load())
		inputX += dx
		inputY += dy
		for nextTick <= now {
			rec.tick++
			c.step(nextTick)
			nextTick += tickStep
		}
	}
	for drained := 0.0; drained < 0.2; drained += tickStep {
		rec.tick++
		if !c.step(nextTick) {
			break
		}
		nextTick += tickStep
	}
	return inputX, inputY
}

// assertBatchedDelivery checks the shared acceptance criteria: synthetic
// delivery stays at most one non-zero call per tick and every input unit is
// either delivered or still pending. requireDelivery additionally asserts the
// replay produced at least one synthetic call.
func assertBatchedDelivery(t *testing.T, c *scrollController, rec *wheelTickRecorder, inputX, inputY float64, requireDelivery bool) {
	t.Helper()
	if requireDelivery && len(rec.subs) == 0 {
		t.Fatal("dense burst delivered nothing")
	}
	if got := rec.maxCallsPerTick(); got > 1 {
		t.Fatalf("synthetic calls in one tick = %d, want at most 1", got)
	}
	for index, sub := range rec.subs {
		if sub.dx == 0 && sub.dy == 0 {
			t.Fatalf("submission %d carried (0,0)", index)
		}
	}
	s := c.session
	if s.kind != scrollSessionWheel || !s.burstActive {
		t.Fatalf("burst not retained after the replay: %+v", s)
	}
	sentX, sentY := rec.total()
	if math.Abs(float64(sentX)+float64(s.queuedX)+s.pendingX-inputX) > 0.01 {
		t.Fatalf("x ledger = %v, want %v", float64(sentX)+float64(s.queuedX)+s.pendingX, inputX)
	}
	if math.Abs(float64(sentY)+float64(s.queuedY)+s.pendingY-inputY) > 0.01 {
		t.Fatalf("y ledger = %v, want %v", float64(sentY)+float64(s.queuedY)+s.pendingY, inputY)
	}
}

func TestEngineWheelDeliversAtMostOneSyntheticCallPerTick(t *testing.T) {
	for _, tickRate := range []int{60, 120, 165} {
		t.Run(fmt.Sprintf("tick-%d", tickRate), func(t *testing.T) {
			rec := &wheelTickRecorder{}
			c, host := newEngineController(nil)
			c.sender = rec.send

			inputX, inputY := denseReplay{tickRate: tickRate, seconds: 0.5, dx: 1, dy: -1}.run(c, host, rec)

			assertBatchedDelivery(t, c, rec, inputX, inputY, true)
		})
	}
}

func TestEngineWheelBatchesDirectionChangesAtFrameBoundaries(t *testing.T) {
	for _, tickRate := range []int{60, 165} {
		t.Run(fmt.Sprintf("tick-%d", tickRate), func(t *testing.T) {
			rec := &wheelTickRecorder{}
			c, host := newEngineController(nil)
			c.sender = rec.send

			cfg := denseReplay{
				tickRate:       tickRate,
				seconds:        0.5,
				dx:             0,
				dy:             -240,
				alternateEvery: int(denseImpulseRate) / tickRate,
			}
			inputX, inputY := cfg.run(c, host, rec)

			assertBatchedDelivery(t, c, rec, inputX, inputY, true)
		})
	}
}

func TestEngineWheelCancelsOppositeImpulsesInsideOneFrame(t *testing.T) {
	// Equal opposite impulses inside one frame may cancel in the queue; what
	// must hold is the frame-level batching and the net displacement.
	for _, tickRate := range []int{60, 165} {
		t.Run(fmt.Sprintf("tick-%d", tickRate), func(t *testing.T) {
			rec := &wheelTickRecorder{}
			c, host := newEngineController(nil)
			c.sender = rec.send

			cfg := denseReplay{tickRate: tickRate, seconds: 0.5, dx: 240, dy: 0, alternateEvery: 1}
			inputX, inputY := cfg.run(c, host, rec)

			assertBatchedDelivery(t, c, rec, inputX, inputY, false)
		})
	}
}

func TestEngineWheelBatchesFractionalInput(t *testing.T) {
	for _, tickRate := range []int{60, 165} {
		t.Run(fmt.Sprintf("tick-%d", tickRate), func(t *testing.T) {
			rec := &wheelTickRecorder{}
			c, host := newEngineController(nil)
			c.sender = rec.send

			inputX, inputY := denseReplay{tickRate: tickRate, seconds: 0.5, dx: 0.25, dy: -0.1}.run(c, host, rec)

			assertBatchedDelivery(t, c, rec, inputX, inputY, true)
		})
	}
}

func TestEngineWheelImpulsesWaitForTheFrameTick(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	stub := &stubTickBackend{}
	c.tickBackend = stub.backend()
	clock := 100.0
	c.now = func() float64 { return clock }

	c.impulseWheel(clock, 10, 20, 1, 0, host, 0, -240, c.epoch.Load())
	if len(stub.regIDs) != 1 {
		t.Fatalf("registrations = %v, want one tick for the burst", stub.regIDs)
	}
	clock = 100.004
	c.impulseWheel(clock, 10, 20, 1, 0, host, 0, -240, c.epoch.Load())
	if len(rec.subs) != 0 {
		t.Fatalf("impulses delivered %d events before the frame tick", len(rec.subs))
	}

	clock = 100.016
	if !c.tickFire(c.tickState.gen) {
		t.Fatal("burst stopped ticking while displacement was pending")
	}
	if len(rec.subs) != 1 {
		t.Fatalf("submissions in the tick = %d, want 1", len(rec.subs))
	}
	if rec.subs[0].dy == 0 {
		t.Fatal("batched submission carried no vertical displacement")
	}
	if c.tickState.id == 0 {
		t.Fatal("tick registration cleared while pending displacement remained")
	}
}

func TestEngineWheelRepeatedStepWithEmptyQueueDoesNotResend(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0, 240, c.epoch.Load())

	c.step(100.05)
	first := len(rec.subs)
	if first == 0 {
		t.Fatal("burst delivered nothing at the frame tick")
	}
	// The same instant repeated must not resend a zero or duplicate batch.
	c.step(100.05)
	if len(rec.subs) != first {
		t.Fatalf("repeated step sent %d extra events", len(rec.subs)-first)
	}
}

func TestEngineWheelFlushesQueuedShareBeforeSettling(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0.25, 0, c.epoch.Load())

	// A ready share must be delivered even though the decaying remainder is
	// already below one unit and would settle on its own.
	c.mu.Lock()
	c.session.queuedX = 3
	c.mu.Unlock()

	if keep := c.step(100.01); keep {
		t.Fatal("settled burst keeps ticking after flushing its queued share")
	}
	if len(rec.subs) != 1 {
		t.Fatalf("submissions = %d, want the single queued share", len(rec.subs))
	}
	if tx, _ := rec.total(); tx != 3 {
		t.Fatalf("delivered total = %d, want 3", tx)
	}
	if c.session.queuedX != 0 {
		t.Fatalf("queued share = %d after flush, want 0", c.session.queuedX)
	}
}

func TestEngineWheelQueueOverflowCancelsTheBurst(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0, -240, c.epoch.Load())

	c.mu.Lock()
	c.session.queuedX = int64(math.MaxInt32) + 1
	c.mu.Unlock()

	if keep := c.step(100.01); keep {
		t.Fatal("burst keeps ticking after a queue overflow")
	}
	if len(rec.subs) != 0 {
		t.Fatalf("overflowing queue submitted %d events", len(rec.subs))
	}
	if c.overloadCompletions != 1 {
		t.Fatalf("overload completions = %d, want 1", c.overloadCompletions)
	}
	if c.session.burstActive || c.session.kind != scrollSessionNone {
		t.Fatalf("session survives a queue overflow: %+v", c.session)
	}
}

func TestEngineWheelQueuedOutputIsDiscardedOnCancellation(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0, -240, c.epoch.Load())

	c.mu.Lock()
	c.session.queuedX, c.session.queuedY = 9, -4
	c.mu.Unlock()
	c.invalidate()

	if c.step(100.01) {
		t.Fatal("invalidated burst keeps ticking")
	}
	if len(rec.subs) != 0 {
		t.Fatalf("cancelled burst submitted %d events", len(rec.subs))
	}
	if c.session.kind != scrollSessionNone || c.session.queuedX != 0 || c.session.queuedY != 0 {
		t.Fatalf("queued output survives cancellation: %+v", c.session)
	}
}

func TestEngineWheelQueuedOutputIsDiscardedOnModifierChange(t *testing.T) {
	rec := &gateRecorder{}
	c, host := newEngineController(rec)
	c.impulseWheel(100.0, 10, 20, 1, 0, host, 0, -240, c.epoch.Load())

	c.mu.Lock()
	c.session.queuedY = -7
	c.mu.Unlock()
	c.noteModifiers(uint(gdk.ShiftMaskValue))

	if c.step(100.01) {
		t.Fatal("interrupted burst keeps ticking")
	}
	if len(rec.subs) != 0 {
		t.Fatalf("interrupted burst submitted %d events", len(rec.subs))
	}
	if c.session.queuedX != 0 || c.session.queuedY != 0 {
		t.Fatalf("queued output transferred across a modifier change: %+v", c.session)
	}
}
