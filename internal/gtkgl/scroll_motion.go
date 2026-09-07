package gtkgl

import (
	"math"
)

// Motion constants for inertial scrolling. These are internal tuning values,
// not user settings; they change only after manual validation (M2).
const (
	// touchpadReleaseTau is the exponential decay constant (seconds) for
	// touchpad release velocity v(t) = v0*exp(-t/tau).
	touchpadReleaseTau = 0.18
	// touchpadReleaseStopVelocity ends release when both absolute axis
	// velocities fall below this many output units/second.
	touchpadReleaseStopVelocity = 5.0
	// touchpadReleaseMaxDuration caps release animation in seconds.
	touchpadReleaseMaxDuration = 1.2
	// wheelSmoothingTau is the exponential approach constant (seconds) for
	// wheel remainder emission emitted = pending*(1-exp(-dt/tau)).
	wheelSmoothingTau = 0.05
	// scrollFrameStallThreshold cancels motion without catch-up when a frame
	// gap exceeds this many seconds.
	scrollFrameStallThreshold = 0.1
	// scrollWheelIdleTimeout ends a wheel burst after this many seconds
	// without impulses; a remainder above one unit then is an explicit
	// overload completion, not conserved displacement.
	scrollWheelIdleTimeout = 0.25
	// scrollOverloadEnvelope bounds supported pending displacement. Beyond it
	// the session overload-cancels instead of looping unbounded integer
	// chunks.
	scrollOverloadEnvelope = float64(int64(1) << 30)
)

// touchpadReleaseStop computes when release motion ends: the earlier of the
// analytic threshold crossing (both axes below stop velocity) and the maximum
// duration. Axes already below threshold contribute no time.
func touchpadReleaseStop(vx, vy float64) float64 {
	stop := 0.0
	for _, v := range [2]float64{vx, vy} {
		av := math.Abs(v)
		if av < touchpadReleaseStopVelocity || !isFinite(v) {
			continue
		}
		t := touchpadReleaseTau * math.Log(av/touchpadReleaseStopVelocity)
		if t > stop {
			stop = t
		}
	}
	if stop <= 0 || math.IsNaN(stop) {
		return 0
	}
	return math.Min(stop, touchpadReleaseMaxDuration)
}

// touchpadIntervalDisplacement integrates v0*exp(-t/tau) over [a, b] in
// seconds for one axis. Callers clip b to the analytic stop so the late frame
// timestamp never extends motion.
func touchpadIntervalDisplacement(v0, a, b float64) float64 {
	if b <= a || v0 == 0 {
		return 0
	}
	return v0 * touchpadReleaseTau * (math.Exp(-a/touchpadReleaseTau) - math.Exp(-b/touchpadReleaseTau))
}

// wheelEmissionShare returns the fraction of pending wheel displacement
// released over dt seconds: 1-exp(-dt/tau).
func wheelEmissionShare(dt float64) float64 {
	if dt <= 0 {
		return 0
	}
	return 1 - math.Exp(-dt/wheelSmoothingTau)
}

// extractWheelChunk converts a float displacement share into an overflow-safe
// integer submission chunk, returning the chunk and the unemitted remainder.
// Conversion truncates toward zero so sign reversals net correctly.
func extractWheelChunk(share float64) (chunk int32, rest float64) {
	if !isFinite(share) {
		return 0, 0
	}
	clamped := math.Max(-scrollOverloadEnvelope, math.Min(scrollOverloadEnvelope, share))
	whole := math.Trunc(clamped)
	return int32(whole), share - whole
}

// pendingOverload reports whether pending displacement exceeds the supported
// envelope on either axis.
func pendingOverload(px, py float64) bool {
	return math.Abs(px) > scrollOverloadEnvelope || math.Abs(py) > scrollOverloadEnvelope
}

// wheelRemainderSettled reports whether undelivered ideal displacement is
// below one output unit on both axes, so integer delivery can finish while
// the session retains its fractional remainder. It measures the ideal
// (pending minus delivery residue), not the ledger balance: the ideal
// decays asymptotically and integer emission stalls below one unit, so
// testing the ledger would livelock instead of settling.
func wheelRemainderSettled(px, py float64) bool {
	return math.Abs(px) < 1 && math.Abs(py) < 1
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
