package gtkgl

// InjectScroll feeds a synthetic scroll impulse (keyboard scrolling, UI
// affordances without scroll events) into the animated wheel engine.
// dx/dy use CEF wheel-delta sign and units so callers forward page-scroll
// deltas unchanged. Repeated calls (key repeat, held keys) join the live
// burst and the release tail coasts after the last impulse; delivery stays
// frozen at the burst origin like wheel input. It reports whether the
// impulse was accepted (false with no host attached). Without
// WheelSmoothing the impulse is delivered directly, preserving legacy
// behavior. Call only on the GTK thread; View.InjectScroll hops threads
// for off-thread callers.
func (ib *InputBridge) InjectScroll(dx, dy float64) bool {
	if ib == nil || ib.scroll == nil {
		return false
	}
	host, x, y, scale, opts, _ := ib.currentScrollState()
	if host == nil {
		return false
	}
	if !isFinite(dx) || !isFinite(dy) || (dx == 0 && dy == 0) {
		return false
	}
	if !opts.WheelSmoothing {
		evt := BuildMouseEvent(x, y, 0, scale)
		host.SendMouseWheelEvent(&evt, int32(dx), int32(dy))
		return true
	}
	ib.scroll.abandonTouch()
	// Current epoch: injection carries no stale check window (no application
	// callback runs between its check and its routing), so the live epoch
	// is exact here; a racing invalidation still retires it via the gate.
	return ib.scroll.impulseWheel(ib.scrollNow(), x, y, scale, 0, host, dx, dy, ib.scroll.epoch.Load())
}
