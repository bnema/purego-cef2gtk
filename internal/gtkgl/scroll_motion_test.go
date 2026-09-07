package gtkgl

import (
	"math"
	"testing"
)

// integrateRelease sums interval displacements over a fixed-step frame grid
// from 0 to the analytic stop, mimicking the engine tick loop.
func integrateRelease(vx, vy, step float64) (dx, dy float64) {
	stop := touchpadReleaseStop(vx, vy)
	for t := 0.0; t < stop; t += step {
		b := math.Min(t+step, stop)
		dx += touchpadIntervalDisplacement(vx, t, b)
		dy += touchpadIntervalDisplacement(vy, t, b)
	}
	return dx, dy
}

func TestTouchpadReleaseTrajectoriesMatchAcrossRefreshRates(t *testing.T) {
	dx60, dy60 := integrateRelease(900, -450, 1.0/60)
	dx120, dy120 := integrateRelease(900, -450, 1.0/120)
	dx144, dy144 := integrateRelease(900, -450, 1.0/144)

	// Analytic total over [0, stop], excluding the discarded post-stop
	// tail: v0*tau*(1-exp(-stop/tau)) per axis.
	stop := touchpadReleaseStop(900, -450)
	wantX := 900 * touchpadReleaseTau * (1 - math.Exp(-stop/touchpadReleaseTau))
	wantY := -450 * touchpadReleaseTau * (1 - math.Exp(-stop/touchpadReleaseTau))
	trajectories := map[string][2]float64{
		"60Hz": {dx60, dy60}, "120Hz": {dx120, dy120}, "144Hz": {dx144, dy144},
	}
	for name, got := range trajectories {
		if math.Abs(got[0]-wantX) > 2 || math.Abs(got[1]-wantY) > 2 {
			t.Fatalf("%s trajectory = (%v,%v), want near (%v,%v)", name, got[0], got[1], wantX, wantY)
		}
	}
	if math.Abs(dx60-dx144) > 0.5 || math.Abs(dy60-dy144) > 0.5 {
		t.Fatalf("refresh-rate divergence: 60Hz=(%v,%v) 144Hz=(%v,%v)", dx60, dy60, dx144, dy144)
	}
	_ = dx120
	_ = dy120
}

func TestTouchpadReleaseIrregularFramesConverge(t *testing.T) {
	vx, vy := 700.0, 300.0
	stop := touchpadReleaseStop(vx, vy)
	// Irregular frame boundaries across the same span.
	bounds := []float64{0, 0.011, 0.029, 0.033, 0.061, 0.09, 0.13, 0.2, 0.31, 0.5, stop}
	var dx, dy float64
	for i := 0; i+1 < len(bounds); i++ {
		dx += touchpadIntervalDisplacement(vx, bounds[i], bounds[i+1])
		dy += touchpadIntervalDisplacement(vy, bounds[i], bounds[i+1])
	}
	wantX := vx * touchpadReleaseTau * (1 - math.Exp(-stop/touchpadReleaseTau))
	wantY := vy * touchpadReleaseTau * (1 - math.Exp(-stop/touchpadReleaseTau))
	if math.Abs(dx-wantX) > 1e-9 || math.Abs(dy-wantY) > 1e-9 {
		t.Fatalf("irregular trajectory = (%v,%v), want (%v,%v)", dx, dy, wantX, wantY)
	}
}

func TestTouchpadReleaseStopBelowThresholdIsZero(t *testing.T) {
	if stop := touchpadReleaseStop(4.9, -4.9); stop != 0 {
		t.Fatalf("stop = %v, want 0 for sub-threshold velocity", stop)
	}
}

func TestTouchpadReleaseStopCapsAtMaxDuration(t *testing.T) {
	if stop := touchpadReleaseStop(1e9, 0); stop != touchpadReleaseMaxDuration {
		t.Fatalf("stop = %v, want cap %v", stop, touchpadReleaseMaxDuration)
	}
}

func TestTouchpadReleaseStopRejectsNonFiniteVelocity(t *testing.T) {
	if stop := touchpadReleaseStop(math.NaN(), math.Inf(1)); stop != 0 {
		t.Fatalf("stop = %v, want 0 for non-finite velocity", stop)
	}
}

func TestTouchpadIntervalDisplacementIgnoresNonpositiveSpan(t *testing.T) {
	if d := touchpadIntervalDisplacement(500, 0.2, 0.2); d != 0 {
		t.Fatalf("zero-span displacement = %v, want 0", d)
	}
	if d := touchpadIntervalDisplacement(500, 0.3, 0.1); d != 0 {
		t.Fatalf("backward-span displacement = %v, want 0", d)
	}
}

func TestWheelEmissionShareBounds(t *testing.T) {
	if s := wheelEmissionShare(0); s != 0 {
		t.Fatalf("zero-dt share = %v, want 0", s)
	}
	if s := wheelEmissionShare(-1); s != 0 {
		t.Fatalf("negative-dt share = %v, want 0", s)
	}
	small := wheelEmissionShare(0.008)
	large := wheelEmissionShare(10)
	if small <= 0 || small >= 0.2 {
		t.Fatalf("8ms share = %v, want small positive fraction", small)
	}
	if large < 0.999 {
		t.Fatalf("10s share = %v, want near 1", large)
	}
}

func TestExtractWheelChunkTruncatesTowardZero(t *testing.T) {
	chunk, rest := extractWheelChunk(2.7)
	if chunk != 2 || math.Abs(rest-0.7) > 1e-9 {
		t.Fatalf("chunk = (%d,%v), want (2,0.7)", chunk, rest)
	}
	chunk, rest = extractWheelChunk(-2.7)
	if chunk != -2 || math.Abs(rest+0.7) > 1e-9 {
		t.Fatalf("chunk = (%d,%v), want (-2,-0.7)", chunk, rest)
	}
}

func TestExtractWheelChunkRejectsNonFinite(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		chunk, rest := extractWheelChunk(v)
		if chunk != 0 || rest != 0 {
			t.Fatalf("non-finite chunk = (%d,%v), want (0,0)", chunk, rest)
		}
	}
}

func TestPendingOverloadEnvelope(t *testing.T) {
	if pendingOverload(100, -200) {
		t.Fatal("ordinary pending flagged as overload")
	}
	if !pendingOverload(scrollOverloadEnvelope+1, 0) {
		t.Fatal("envelope breach not flagged")
	}
	if !pendingOverload(0, -(scrollOverloadEnvelope + 1)) {
		t.Fatal("negative envelope breach not flagged")
	}
}

func TestWheelEmissionFollowsAnalyticOracle(t *testing.T) {
	run := func(dts []float64, impulses ...float64) (ideal, pending float64) {
		rec := &gateRecorder{}
		c, host := newEngineController(rec)
		t := 100.0
		for _, f := range impulses {
			c.impulseWheel(t, 10, 20, 1, 0, host, f, 0)
		}
		for _, dt := range dts {
			t += dt
			c.step(t)
		}
		s := c.session
		return s.pendingX - s.resX, s.pendingX
	}
	grid := func(dt float64, n int) []float64 {
		dts := make([]float64, n)
		for i := range dts {
			dts[i] = dt
		}
		return dts
	}
	// Analytic oracle: ideal decays exactly as P0*exp(-T/tau) over the
	// stepped spans; only integer quantization stays in pending.
	for name, dts := range map[string][]float64{
		"60Hz":      grid(1.0/60, 10),
		"120Hz":     grid(1.0/120, 20),
		"144Hz":     grid(1.0/144, 24),
		"irregular": {0.011, 0.029, 0.004, 0.028, 0.029, 0.04, 0.07},
	} {
		total := 0.0
		for _, dt := range dts {
			total += dt
		}
		ideal, _ := run(dts, 90)
		want := 90 * math.Exp(-total/wheelSmoothingTau)
		if math.Abs(ideal-want) > 1e-6 {
			t.Fatalf("%s ideal = %v, want oracle %v", name, ideal, want)
		}
	}
	// Fractional impulses and reversal track the signed oracle.
	ideal, _ := run(grid(1.0/60, 10), 30.5, -10.25)
	want := 20.25 * math.Exp(-(10.0/60)/wheelSmoothingTau)
	if math.Abs(ideal-want) > 1e-6 {
		t.Fatalf("reversal ideal = %v, want oracle %v", ideal, want)
	}
}

func TestWheelRemainderSettled(t *testing.T) {
	if !wheelRemainderSettled(0.9, -0.5) {
		t.Fatal("sub-unit remainder not settled")
	}
	if wheelRemainderSettled(1.0, 0) {
		t.Fatal("exactly-one-unit remainder settled")
	}
}
