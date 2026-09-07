package gtkgl

import (
	"time"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gtk"
)

// scrollSessionKind identifies the animated motion owned by a session.
type scrollSessionKind int

const (
	scrollSessionNone scrollSessionKind = iota
	scrollSessionTouchpad
	scrollSessionWheel
)

// animatedScrollClass is the animation input class of an update.
type animatedScrollClass int

const (
	scrollClassNone animatedScrollClass = iota
	scrollClassTouchpad
	scrollClassWheel
)

// scrollSession holds animated motion for one gesture (touchpad) or burst
// (wheel). Residual float state belongs to the session and is discarded only
// at session termination or explicit overload cancellation.
type scrollSession struct {
	epoch   uint64
	kind    scrollSessionKind
	host    cef.BrowserHost
	x, y    float64
	scale   float64
	mods    uint
	precise bool
	// Touchpad direct phase.
	touchActive      bool
	accepted         int
	poisoned         bool
	navigated        bool
	awaitingVelocity bool
	// Touchpad release phase.
	releasing  bool
	vx, vy     float64
	t0         float64
	lastT      float64
	stopT      float64
	resX, resY float64
	// Wheel burst phase.
	burstActive        bool
	pendingX, pendingY float64
	lastImpulseT       float64
	lastStepT          float64
	lastDeliveryT      float64
	hasDelivery        bool
	hasClock           bool
	// sentX/sentY tally dispatched integers for the trace.
	sentX, sentY int64
}

// scrollTickBackend wires frame scheduling to the owning widget. Tests leave
// it nil and drive step manually; the bridge installs widget-backed
// callbacks on attach.
type scrollTickBackend struct {
	registrar func(*gtk.TickCallback) uint
	remover   func(uint)
	unrefer   func(*gtk.TickCallback)
}

// scrollTickState tracks at most one registered scroll tick. The callback
// slot is retained for the controller lifetime and unreferenced only during
// safe teardown, never from inside its executing trampoline.
type scrollTickState struct {
	cb  *gtk.TickCallback
	id  uint
	gen uint64
}

// scrollClockBase anchors the monotonic engine clock. All session
// timestamps (physical events and frame ticks) share this domain so event
// intervals stay live even when no GTK frames render between input events.
// Never reset it: sessions compare instants across attaches and gestures.
var scrollClockBase = time.Now()

func wallClockSeconds() float64 {
	return time.Since(scrollClockBase).Seconds()
}

func defaultWheelSender(host cef.BrowserHost, evt *cef.MouseEvent, dx, dy int32) {
	host.SendMouseWheelEvent(evt, dx, dy)
}

// invalidate immediately retires pending motion and release eligibility. It
// is lock-free (single atomic epoch bump) so off-thread paths and reentrant
// CEF callbacks can never deadlock against the submission gate. It returns
// the retired epoch: the epoch the live session carried. Pass that epoch to
// cleanupEpoch for GTK cleanup of the old tick and session state.
func (c *scrollController) invalidate() uint64 {
	if c == nil {
		return 0
	}
	return c.epoch.Add(1) - 1
}

// cleanupEpoch performs GTK-only cleanup for a retired epoch. It never
// clears a newer session: when the session epoch differs, a fresh gesture
// owns the controller (or cleanup already ran) and the stale cleanup is a
// no-op.
func (c *scrollController) cleanupEpoch(epoch uint64) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session.epoch != epoch {
		c.tracef("cleanup-skip epoch=%d live=%d", epoch, c.session.epoch)
		return false
	}
	c.tracef("cleanup-run epoch=%d kind=%d", epoch, c.session.kind)
	c.session = scrollSession{}
	c.stopTickLocked()
	return true
}

// cancelNow is the GTK-thread synchronous path: invalidate plus immediate
// cleanup of the current session.
func (c *scrollController) cancelNow() {
	if c == nil {
		return
	}
	retired := c.invalidate()
	c.tracef("cancel-now retired=%d", retired)
	c.cleanupEpoch(retired)
}

// suppressRelease poisons touchpad release eligibility without retiring the
// session epoch, used before a recognized built-in navigation action fires.
func (c *scrollController) suppressRelease() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.session.poisoned = true
	c.session.navigated = true
	c.session.awaitingVelocity = false
	c.session.releasing = false
	c.mu.Unlock()
}

// submitGated is the single submission gate for animated output. It holds the
// controller lock across validation and the CEF call so a racing invalidation
// cannot slip a stale submission through after cancellation takes effect.
//
// Deadlock analysis: the generated purego-cef binding invokes libcef's input
// entry directly (a C call with no synchronous Go callbacks; CEF routes wheel
// input asynchronously), application OnScroll callbacks are always invoked
// without this lock, and invalidate() never acquires it. A reentrant CEF
// callback therefore cannot block on this gate.
//
// Linearization: the epoch check inside the lock is the submission's commit
// point and the atomic bump is cancellation's. A submission that validates
// before the bump is already in flight (already-submitted CEF input, which
// cannot be retracted); once the bump is observed every later validation
// fails, so no new submission from the invalidated session can occur.
func (c *scrollController) submitGated(epoch uint64, host cef.BrowserHost, evt *cef.MouseEvent, dx, dy int32, now float64) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.submitLocked(epoch, host, evt, dx, dy, now)
}

func (c *scrollController) submitLocked(epoch uint64, host cef.BrowserHost, evt *cef.MouseEvent, dx, dy int32, now float64) bool {
	if epoch != c.epoch.Load() {
		c.tracef("submit-reject stale-epoch want=%d have=%d dx=%d dy=%d", epoch, c.epoch.Load(), dx, dy)
		return false
	}
	if host == nil || !sameBrowserHost(c.session.host, host) {
		c.tracef("submit-reject host-mismatch dx=%d dy=%d", dx, dy)
		return false
	}
	sender := c.sender
	if sender == nil {
		sender = defaultWheelSender
	}
	sender(host, evt, dx, dy)
	c.session.sentX += int64(dx)
	c.session.sentY += int64(dy)
	c.session.lastDeliveryT = now
	c.session.hasDelivery = true
	return true
}

// submitPhysical submits one direct physical update through the gate. Unlike
// synthetic submissions it checks only the epoch: host replacement and
// detach always invalidate first, so the epoch alone retires stale physical
// delivery without dropping legitimate updates after a session clear.
func (c *scrollController) submitPhysical(epoch uint64, host cef.BrowserHost, evt *cef.MouseEvent, dx, dy int32, now float64) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if epoch != c.epoch.Load() || host == nil {
		return false
	}
	sender := c.sender
	if sender == nil {
		sender = defaultWheelSender
	}
	sender(host, evt, dx, dy)
	if c.session.epoch == epoch {
		c.session.sentX += int64(dx)
		c.session.sentY += int64(dy)
		c.session.lastDeliveryT = now
		c.session.hasDelivery = true
	}
	return true
}

// animatedClass resolves the animation input class for an update. Modified
// action/zoom input (Ctrl/Alt/Meta) always bypasses animation with physical
// delivery; disabled options keep legacy direct behavior.
func (c *scrollController) animatedClass(opts ScrollOptions, unit gdk.ScrollUnit, unitKnown bool, mods uint) animatedScrollClass {
	if c == nil {
		return scrollClassNone
	}
	if mods&(uint(gdk.ControlMaskValue)|uint(gdk.AltMaskValue)|uint(gdk.MetaMaskValue)) != 0 {
		return scrollClassNone
	}
	if unitKnown && unit == gdk.ScrollUnitSurfaceValue {
		if opts.TouchpadInertia {
			return scrollClassTouchpad
		}
		return scrollClassNone
	}
	if opts.WheelSmoothing {
		return scrollClassWheel
	}
	return scrollClassNone
}

// beginTouch starts a fresh touchpad gesture, invalidating previous touch
// motion before the application is notified. A live wheel burst is
// preserved: the gesture's own updates reclassify it on arrival (surface
// updates take over the session, wheel updates join the burst), so a begin
// signal from a mixed device stream never discards motion prematurely.
// Callers notify the application after this returns.
func (c *scrollController) beginTouch(x, y float64, scale float64, mods uint, host cef.BrowserHost) uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	if s := &c.session; s.kind == scrollSessionWheel && s.epoch == c.epoch.Load() && s.burstActive {
		// A live wheel burst survives the begin signal without even
		// retiring its epoch; the gesture's own updates reclassify the
		// session on arrival.
		epoch := s.epoch
		c.mu.Unlock()
		return epoch
	}
	c.mu.Unlock()
	c.invalidate()
	epoch := c.epoch.Load()
	c.mu.Lock()
	c.session = scrollSession{
		epoch:       epoch,
		kind:        scrollSessionTouchpad,
		host:        host,
		x:           x,
		y:           y,
		scale:       scale,
		mods:        mods,
		precise:     true,
		touchActive: true,
	}
	c.mu.Unlock()
	return epoch
}

// ensureTouchSession starts fresh direct-phase touch tracking when no live
// touch gesture owns the controller, discarding any wheel burst
// intentionally. It never bumps the epoch: a stale tick revalidates the
// session kind and stops quietly, so no cross-thread invalidation is needed
// for same-thread input-class switches.
func (c *scrollController) ensureTouchSession(x, y float64, scale float64, mods uint, host cef.BrowserHost) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &c.session
	if s.kind == scrollSessionTouchpad && s.epoch == c.epoch.Load() &&
		(s.touchActive || s.awaitingVelocity || s.releasing) {
		return
	}
	if s.kind == scrollSessionWheel && s.burstActive {
		c.stopTickLocked()
	}
	*s = scrollSession{
		epoch:       c.epoch.Load(),
		kind:        scrollSessionTouchpad,
		host:        host,
		x:           x,
		y:           y,
		scale:       scale,
		mods:        mods,
		precise:     true,
		touchActive: true,
	}
}

// updateTouch records a direct-phase touchpad update. It returns whether the
// update was accepted for release eligibility. Consumed updates poison the
// whole gesture; modifier changes terminate it.
func (c *scrollController) updateTouch(unit gdk.ScrollUnit, unitKnown bool, consumed bool, mods uint, x, y float64) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &c.session
	if s.kind != scrollSessionTouchpad || s.epoch != c.epoch.Load() || !s.touchActive {
		return false
	}
	if mods != s.mods {
		c.tracef("touch-mods-kill old=%x new=%x", s.mods, mods)
		*s = scrollSession{}
		return false
	}
	if consumed {
		c.tracef("touch-consumed")
		s.poisoned = true
		return false
	}
	if !unitKnown || unit != gdk.ScrollUnitSurfaceValue {
		c.tracef("touch-class-end")
		s.touchActive = false
		return false
	}
	s.accepted++
	s.x, s.y = x, y
	return true
}

// endTouch closes the direct phase. Release starts only when a later
// decelerate supplies velocity for an eligible gesture.
func (c *scrollController) endTouch() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &c.session
	if s.kind != scrollSessionTouchpad || s.epoch != c.epoch.Load() || !s.touchActive {
		return
	}
	s.touchActive = false
	if s.poisoned || s.navigated || s.accepted == 0 {
		c.tracef("touch-end no-release poisoned=%v navigated=%v accepted=%d", s.poisoned, s.navigated, s.accepted)
		return
	}
	s.awaitingVelocity = true
	c.tracef("touch-end awaiting-velocity")
}

// releaseFromDecelerate arms release motion from GTK release velocity in
// scroll-delta units/second. Per verified GTK 4.22.4 semantics these are
// already units/s: axis signs and multipliers apply once, with no extra
// factor of 1000. A late decelerate from an invalidated gesture, a consumed
// or navigated gesture, a hold-only gesture, or invalid velocity never
// restarts motion.
func (c *scrollController) releaseFromDecelerate(now float64, x, y float64, scale float64, mods uint, host cef.BrowserHost, vx, vy float64, opts ScrollOptions) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &c.session
	if s.kind != scrollSessionTouchpad || s.epoch != c.epoch.Load() || !s.awaitingVelocity {
		c.tracef("release-reject no-await kind=%d", s.kind)
		return false
	}
	s.awaitingVelocity = false
	if s.poisoned || s.navigated || !isFinite(vx) || !isFinite(vy) {
		c.tracef("release-reject poisoned=%v navigated=%v finite=%v", s.poisoned, s.navigated, isFinite(vx) && isFinite(vy))
		s.releasing = false
		return false
	}
	precise := normalizePreciseMultiplier(opts.PreciseMultiplier)
	horizontal := normalizeMultiplier(opts.HorizontalMultiplier)
	vertical := normalizeMultiplier(opts.VerticalMultiplier)
	ovx := vx * precise * horizontal
	ovy := -vy * precise * vertical
	stop := touchpadReleaseStop(ovx, ovy)
	if stop <= 0 {
		c.tracef("release-reject sub-threshold v=(%.1f,%.1f)", ovx, ovy)
		return false
	}
	s.releasing = true
	s.vx, s.vy = ovx, ovy
	s.t0, s.lastT = now, now
	s.stopT = stop
	s.resX, s.resY = 0, 0
	s.host = host
	s.x, s.y = x, y
	s.scale = scale
	s.mods = mods
	s.precise = true
	s.hasClock = true
	c.tracef("release-arm v=(%.1f,%.1f) stop=%.3f", ovx, ovy, stop)
	c.ensureTickLocked()
	return true
}

// abandonTouch discards touchpad session state on input-class change.
func (c *scrollController) abandonTouch() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session.kind != scrollSessionTouchpad {
		return
	}
	c.tracef("abandon-touch")
	c.session = scrollSession{}
	c.stopTickLocked()
}

// wheelFreshLocked reports whether an impulse starts a new burst and why:
// new (no live wheel burst), idle (past the idle deadline on a live burst),
// mods, or host mismatch. An idle burst retires through
// endBurstLocked so overload accounting applies; other switches discard
// pending intentionally. Pointer position never starts a fresh burst:
// delivery stays at the frozen burst origin while the burst is live.
func (c *scrollController) wheelFreshLocked(s *scrollSession, now float64, mods uint, host cef.BrowserHost) (bool, string) {
	if s.kind != scrollSessionWheel || s.epoch != c.epoch.Load() || !s.burstActive {
		return true, "new"
	}
	if (now - s.lastImpulseT) > scrollWheelIdleTimeout {
		return true, "idle"
	}
	if mods != s.mods {
		return true, "mods"
	}
	if !sameBrowserHost(s.host, host) {
		return true, "host"
	}
	return false, ""
}

// impulseWheel accumulates one accepted wheel impulse, advancing the burst
// clock to the event time and emitting the accrued share before adding the
// impulse. Same-session reversal nets against pending displacement through
// plain addition. Bursts end (discarding pending intentionally) on session
// changes, stalls, modifier or host mismatch, and the idle deadline.
// Pointer motion never splits a burst: every impulse joins the live burst
// and delivery stays at the frozen origin.
func (c *scrollController) impulseWheel(now float64, x, y float64, scale float64, mods uint, host cef.BrowserHost, fx, fy float64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !isFinite(fx) || !isFinite(fy) {
		// Non-finite impulses never enter motion state; the session is
		// left untouched rather than poisoned with NaN displacement.
		return
	}
	s := &c.session
	fresh, reason := c.wheelFreshLocked(s, now, mods, host)
	if fresh {
		if reason == "idle" {
			c.endBurstLocked(now, s)
		}
		c.tracef("burst-fresh reason=%s discarded=(%.1f,%.1f) sent=(%d,%d)", reason, s.pendingX, s.pendingY, s.sentX, s.sentY)
		*s = scrollSession{
			epoch:       c.epoch.Load(),
			kind:        scrollSessionWheel,
			host:        host,
			x:           x,
			y:           y,
			scale:       scale,
			mods:        mods,
			precise:     true,
			burstActive: true,
			lastStepT:   now,
			hasClock:    true,
		}
		s = &c.session
	} else if s.hasClock && now-s.lastStepT > scrollFrameStallThreshold {
		// A long stall cancels without catch-up: discard, then treat this
		// impulse as the start of fresh motion within the same burst origin.
		s.pendingX, s.pendingY = 0, 0
		s.resX, s.resY = 0, 0
		s.lastStepT = now
	}
	if s.hasClock {
		c.emitWheelShareLocked(s, now)
	}
	s.pendingX += fx
	s.pendingY += fy
	s.lastImpulseT = now
	s.lastStepT = now
	c.tracef("impulse t=%.3f f=(%.1f,%.1f) pending=(%.1f,%.1f) res=(%.2f,%.2f)", now, fx, fy, s.pendingX, s.pendingY, s.resX, s.resY)
	if pendingOverload(s.pendingX, s.pendingY) {
		c.overloadCompletions++
		s.pendingX, s.pendingY = 0, 0
		s.burstActive = false
		c.stopTickLocked()
		return
	}
	c.ensureTickLocked()
}

// endBurstLocked retires a wheel burst. A remainder above one unit past the
// idle deadline is an explicit overload completion, never conserved motion.
func (c *scrollController) endBurstLocked(now float64, s *scrollSession) {
	if s.kind != scrollSessionWheel || !s.burstActive {
		return
	}
	if (now-s.lastImpulseT) > scrollWheelIdleTimeout && !wheelRemainderSettled(s.pendingX-s.resX, s.pendingY-s.resY) {
		c.overloadCompletions++
	}
	s.burstActive = false
	s.pendingX, s.pendingY = 0, 0
}

// emitWheelShareLocked submits the accrued integer share of pending
// displacement. Only dispatched integers leave pending; the unsubmitted
// fraction carries in the session residual so decay always progresses and
// small emissions are never silently dropped. The leftover fraction stays
// with the session as the retained remainder.
func (c *scrollController) emitWheelShareLocked(s *scrollSession, now float64) {
	dt := now - s.lastStepT
	if dt <= 0 {
		return
	}
	share := wheelEmissionShare(dt)
	// Decay the ideal float target; the delivery residual only rejoins for
	// integer quantization and never feeds the next decay step.
	idealX := s.pendingX - s.resX
	idealY := s.pendingY - s.resY
	ex := idealX*share + s.resX
	ey := idealY*share + s.resY
	cx, rx := extractWheelChunk(ex)
	cy, ry := extractWheelChunk(ey)
	s.pendingX -= float64(cx)
	s.pendingY -= float64(cy)
	s.resX, s.resY = rx, ry
	if cx == 0 && cy == 0 {
		c.tracef("wheel-emit dt=%.4f emitted=(0,0) pending=(%.1f,%.1f)", dt, s.pendingX, s.pendingY)
		return
	}
	evt := BuildMouseEvent(s.x, s.y, s.mods, s.scale)
	if s.precise {
		evt.Modifiers |= uint32(cef.EventFlagsEventflagPrecisionScrollingDelta)
	}
	c.tracef("wheel-emit dt=%.4f emitted=(%d,%d) pending=(%.1f,%.1f) res=(%.2f,%.2f)", dt, cx, cy, s.pendingX, s.pendingY, s.resX, s.resY)
	// Session epoch, not reloaded current: a racing invalidation
	// must reject this submission.
	c.submitLocked(s.epoch, s.host, &evt, cx, cy, now)
}

// step advances animated motion to now (seconds) and reports whether frame
// scheduling must continue. It runs on the GTK thread from the tick
// trampoline; tests drive it directly with synthetic clocks.
func (c *scrollController) step(now float64) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &c.session
	if s.kind == scrollSessionNone || s.epoch != c.epoch.Load() {
		*s = scrollSession{}
		c.stopTickLocked()
		return false
	}
	switch s.kind {
	case scrollSessionTouchpad:
		return c.stepReleaseLocked(s, now)
	case scrollSessionWheel:
		return c.stepWheelLocked(s, now)
	default:
		*s = scrollSession{}
		c.stopTickLocked()
		return false
	}
}

func (c *scrollController) stepReleaseLocked(s *scrollSession, now float64) bool {
	if s.touchActive || !s.releasing {
		// The direct phase owns no tick; a stray tick stops quietly and
		// leaves the live session alone.
		c.stopTickLocked()
		return false
	}
	if !s.hasClock {
		c.tracef("release-no-clock")
		*s = scrollSession{}
		c.stopTickLocked()
		return false
	}
	if now-s.lastT > scrollFrameStallThreshold {
		c.tracef("release-stall gap=%.3f sent=(%d,%d)", now-s.lastT, s.sentX, s.sentY)
		*s = scrollSession{}
		c.stopTickLocked()
		return false
	}
	a := s.lastT - s.t0
	b := now - s.t0
	if b > s.stopT {
		b = s.stopT
	}
	if b > a {
		fx := touchpadIntervalDisplacement(s.vx, a, b) + s.resX
		fy := touchpadIntervalDisplacement(s.vy, a, b) + s.resY
		cx, rx := extractWheelChunk(fx)
		cy, ry := extractWheelChunk(fy)
		s.resX, s.resY = rx, ry
		if cx != 0 || cy != 0 {
			evt := BuildMouseEvent(s.x, s.y, s.mods, s.scale)
			if s.precise {
				evt.Modifiers |= uint32(cef.EventFlagsEventflagPrecisionScrollingDelta)
			}
			// Session epoch, not reloaded current: a racing invalidation
			// must reject this submission.
			c.submitLocked(s.epoch, s.host, &evt, cx, cy, now)
		}
		s.lastT = s.t0 + b
	}
	if now-s.t0 >= s.stopT {
		c.tracef("release-done sent=(%d,%d) dropped-res=(%.2f,%.2f)", s.sentX, s.sentY, s.resX, s.resY)
		*s = scrollSession{}
		c.stopTickLocked()
		return false
	}
	return true
}

func (c *scrollController) stepWheelLocked(s *scrollSession, now float64) bool {
	if !s.burstActive {
		*s = scrollSession{}
		c.stopTickLocked()
		return false
	}
	if (now - s.lastImpulseT) > scrollWheelIdleTimeout {
		px, py := s.pendingX, s.pendingY
		c.endBurstLocked(now, s)
		c.tracef("burst-idle discarded=(%.1f,%.1f) sent=(%d,%d) overloads=%d", px, py, s.sentX, s.sentY, c.overloadCompletions)
		*s = scrollSession{}
		c.stopTickLocked()
		return false
	}
	if s.hasClock && now-s.lastStepT > scrollFrameStallThreshold {
		c.tracef("burst-stall gap=%.3f discarded=(%.1f,%.1f) sent=(%d,%d)", now-s.lastStepT, s.pendingX, s.pendingY, s.sentX, s.sentY)
		s.pendingX, s.pendingY = 0, 0
		*s = scrollSession{}
		c.stopTickLocked()
		return false
	}
	if s.hasClock {
		c.emitWheelShareLocked(s, now)
		s.lastStepT = now
	}
	if wheelRemainderSettled(s.pendingX-s.resX, s.pendingY-s.resY) {
		// Integer delivery is finished; the fractional remainder stays with
		// the session until the idle deadline or the next impulse resumes
		// the burst. No tick is needed while nothing integer can emit.
		c.tracef("burst-settled pending=(%.2f,%.2f) res=(%.2f,%.2f) sent=(%d,%d)", s.pendingX, s.pendingY, s.resX, s.resY, s.sentX, s.sentY)
		c.stopTickLocked()
		return false
	}
	return true
}

// noteModifiers terminates animated motion when the modifier state changes
// without follow-up scroll, observed through key press/release handlers.
func (c *scrollController) noteModifiers(mods uint) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &c.session
	if s.kind == scrollSessionNone || s.epoch != c.epoch.Load() {
		return
	}
	if mods == s.mods {
		return
	}
	c.tracef("session-kill mods old=%x new=%x kind=%d discarded=(%.1f,%.1f) sent=(%d,%d)", s.mods, mods, s.kind, s.pendingX, s.pendingY, s.sentX, s.sentY)
	*s = scrollSession{}
	c.stopTickLocked()
}

// (removed: physical deliveries record through the submission gate)

// ensureTickLocked registers the retained tick callback when animation needs
// frame scheduling. Without a backend (unit tests) stepping is manual.
func (c *scrollController) ensureTickLocked() {
	if c.tickBackend == nil || c.tickState.id != 0 {
		return
	}
	if c.tickState.cb == nil {
		c.tickState.cb = new(gtk.TickCallback)
	}
	c.tickState.gen++
	gen := c.tickState.gen
	cb := c.tickState.cb
	// Refresh the trampoline generation so a stale callback cannot
	// clear its replacement tick ID.
	*cb = func(_, _, _ uintptr) bool {
		return c.tickFire(gen)
	}
	c.tickState.id = c.tickBackend.registrar(cb)
	c.tracef("tick-ensure id=%d gen=%d", c.tickState.id, gen)
}

// stopTickLocked removes the tick registration without releasing the
// retained callback slot; teardown owns the unref.
func (c *scrollController) stopTickLocked() {
	if c.tickBackend == nil || c.tickState.id == 0 {
		return
	}
	c.tracef("tick-stop id=%d", c.tickState.id)
	c.tickBackend.remover(c.tickState.id)
	c.tickState.id = 0
	c.tickState.gen++
}

// teardownTick stops scheduling and releases the retained callback slot.
// It runs during safe teardown (detach), never inside the trampoline.
func (c *scrollController) teardownTick() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopTickLocked()
	if c.tickState.cb != nil && c.tickBackend != nil && c.tickBackend.unrefer != nil {
		c.tickBackend.unrefer(c.tickState.cb)
	}
	c.tickState.cb = nil
	c.tickBackend = nil
}

// tickFire drives one frame from the GTK tick trampoline.
func (c *scrollController) tickFire(gen uint64) bool {
	if c == nil {
		return false
	}
	now := wallClockSeconds()
	if c.now != nil {
		now = c.now()
	}
	c.tracef("tick-fire gen=%d t=%.3f", gen, now)
	keep := c.step(now)
	if !keep {
		c.mu.Lock()
		if c.tickState.gen == gen {
			c.tickState.id = 0
		}
		c.mu.Unlock()
	}
	return keep
}
