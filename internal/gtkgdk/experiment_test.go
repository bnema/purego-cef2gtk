package gtkgdk

import (
	"testing"

	"github.com/bnema/puregotk/v4/glib"
)

func TestGraphicsOffloadDefaultsToEnabled(t *testing.T) {
	t.Setenv(GraphicsOffloadEnvVar, "")
	if !GraphicsOffloadEnabled() {
		t.Fatal("graphics offload should default to enabled")
	}
	for _, value := range []string{"0", "false", "no", "off", "OFF"} {
		t.Setenv(GraphicsOffloadEnvVar, value)
		if GraphicsOffloadEnabled() {
			t.Fatalf("graphics offload enabled for %q, want disabled", value)
		}
	}
	for _, value := range []string{"1", "true", "yes", "on"} {
		t.Setenv(GraphicsOffloadEnvVar, value)
		if !GraphicsOffloadEnabled() {
			t.Fatalf("graphics offload disabled for %q, want enabled", value)
		}
	}
}

func TestImportPriorityDefaultsToOrdinaryPriority(t *testing.T) {
	t.Setenv(ImportPriorityEnvVar, "")
	if got := importPriority(); got != glibPriorityDefault {
		t.Fatalf("import priority = %d, want %d", got, glibPriorityDefault)
	}
	t.Setenv(ImportPriorityEnvVar, "idle")
	if got := importPriority(); got != glibPriorityDefaultIdle {
		t.Fatalf("import priority = %d, want %d", got, glibPriorityDefaultIdle)
	}
	t.Setenv(ImportPriorityEnvVar, "anything-else")
	if got := importPriority(); got != glibPriorityDefault {
		t.Fatalf("import priority = %d, want the default %d", got, glibPriorityDefault)
	}
}

func TestRetiredTextureLimitFromEnvClampsAndFallsBack(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{name: "unset", raw: "", want: defaultRetiredTextureLimit},
		{name: "explicit", raw: "4", want: 4},
		{name: "minimum", raw: "1", want: 1},
		{name: "clamped to storage", raw: "99", want: retiredTextureStorage},
		{name: "zero falls back", raw: "0", want: defaultRetiredTextureLimit},
		{name: "negative falls back", raw: "-3", want: defaultRetiredTextureLimit},
		{name: "unparsable falls back", raw: "many", want: defaultRetiredTextureLimit},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(RetiredTexturesEnvVar, testCase.raw)
			if got := retiredTextureLimitFromEnv(); got != testCase.want {
				t.Fatalf("retired texture limit = %d, want %d", got, testCase.want)
			}
		})
	}
}

func TestSchedulePendingImportPassesTheConfiguredPriority(t *testing.T) {
	for name, priority := range map[string]int{
		"default": glibPriorityDefault,
		"idle":    glibPriorityDefaultIdle,
	} {
		t.Run(name, func(t *testing.T) {
			var observed []int
			var callbacks []*glib.SourceOnceFunc
			r := &Renderer{
				importPriority: priority,
				idleAddOnce: func(got int, callback *glib.SourceOnceFunc, _ uintptr) uint {
					observed = append(observed, got)
					callbacks = append(callbacks, callback)
					return uint(len(observed))
				},
			}
			defer r.InvalidateOnGTKThread()

			r.enqueueOwnedFrame(&ownedFrame{Plane: ownedPlane{FD: -1}})
			r.pendingMu.Lock()
			r.pendingScheduledAt = r.pendingScheduledAt.Add(-stalePendingFrameWait - 1)
			r.pendingMu.Unlock()
			r.enqueueOwnedFrame(&ownedFrame{Plane: ownedPlane{FD: -1}})

			if len(observed) != 2 {
				t.Fatalf("schedules = %d, want 2", len(observed))
			}
			for index, got := range observed {
				if got != priority {
					t.Fatalf("schedule %d priority = %d, want %d", index, got, priority)
				}
				if callbacks[index] == nil {
					t.Fatalf("schedule %d handed over a nil callback", index)
				}
			}
		})
	}
}

// glibRuntimeLoadable reports whether the GLib shared library can be resolved in
// this environment.
func glibRuntimeLoadable() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	glib.MainContextDefault()
	return true
}

func TestScheduleIdleOnceAdapterRunsOnceAndRemovesItsSource(t *testing.T) {
	if !glibRuntimeLoadable() {
		t.Skip("GLib runtime unavailable")
	}
	const wantData = uintptr(42)
	for name, priority := range map[string]int{
		"default": glibPriorityDefault,
		"idle":    glibPriorityDefaultIdle,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := glib.MainContextDefault()
			calls := 0
			cb := glib.SourceOnceFunc(func(data uintptr) {
				if data != wantData {
					t.Errorf("callback data = %d, want %d", data, wantData)
				}
				calls++
			})
			sourceID := (&Renderer{}).scheduleIdleOnce(priority, &cb, wantData)
			if sourceID == 0 {
				t.Fatal("scheduleIdleOnce registered no source")
			}
			t.Cleanup(func() {
				// A one-shot source is destroyed by its dispatch; destroy it
				// explicitly only if the test failed before the dispatch.
				if source := ctx.FindSourceById(sourceID); source != nil {
					source.Destroy()
				}
			})

			// The idle source is ready, so iteration cannot block. The bound
			// protects against an unrelated ready source starving this one.
			for iterations := 0; calls == 0 && ctx.Pending() && iterations < 64; iterations++ {
				ctx.Iteration(false)
			}
			if calls != 1 {
				t.Fatalf("callback ran %d times, want exactly 1", calls)
			}
			if source := ctx.FindSourceById(sourceID); source != nil {
				t.Fatalf("source %d still attached after the callback returned false", sourceID)
			}
		})
	}
}
