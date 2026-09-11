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
	var observed []int
	r := &Renderer{
		importPriority: glibPriorityDefault,
		idleAddOnce: func(priority int, _ *glib.SourceOnceFunc, _ uintptr) uint {
			observed = append(observed, priority)
			return 1
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
	for index, priority := range observed {
		if priority != glibPriorityDefault {
			t.Fatalf("schedule %d priority = %d, want %d", index, priority, glibPriorityDefault)
		}
	}
}
