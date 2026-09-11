package gl

import (
	"errors"
	"testing"
	"time"
)

func TestFenceUnsupportedWithoutEntryPoints(t *testing.T) {
	loader := &Loader{}
	if loader.FenceSupported() {
		t.Fatal("FenceSupported() = true without entry points")
	}
	if _, err := loader.Fence(); !errors.Is(err, ErrFenceUnsupported) {
		t.Fatalf("Fence() error = %v, want ErrFenceUnsupported", err)
	}
	if ok, err := loader.WaitFence(1, time.Millisecond); ok || !errors.Is(err, ErrFenceUnsupported) {
		t.Fatalf("WaitFence() = (%t, %v), want (false, ErrFenceUnsupported)", ok, err)
	}
	// Deleting an unknown fence must not panic.
	loader.DeleteFence(1)
	var nilLoader *Loader
	nilLoader.DeleteFence(1)
}

func TestFenceInsertAndDelete(t *testing.T) {
	created, deleted := 0, 0
	loader := &Loader{
		fenceSync:      func(condition uint32, flags uint32) uintptr { created++; return 0x1234 },
		clientWaitSync: func(uintptr, uint32, uint64) uint32 { return ConditionSatisfied },
		deleteSync:     func(uintptr) { deleted++ },
	}
	sync, err := loader.Fence()
	if err != nil {
		t.Fatalf("Fence() error = %v", err)
	}
	if sync != 0x1234 || created != 1 {
		t.Fatalf("Fence() = %#x after %d insertions, want 0x1234 after 1", sync, created)
	}
	loader.DeleteFence(sync)
	if deleted != 1 {
		t.Fatalf("delete calls = %d, want 1", deleted)
	}
}

func TestFenceZeroHandleIsUnsupported(t *testing.T) {
	loader := &Loader{
		fenceSync:      func(uint32, uint32) uintptr { return 0 },
		clientWaitSync: func(uintptr, uint32, uint64) uint32 { return ConditionSatisfied },
		deleteSync:     func(uintptr) {},
	}
	if _, err := loader.Fence(); !errors.Is(err, ErrFenceUnsupported) {
		t.Fatalf("Fence() with a zero handle returned %v, want ErrFenceUnsupported", err)
	}
}

func TestWaitFenceMapsDriverResults(t *testing.T) {
	cases := []struct {
		name      string
		status    uint32
		wantOK    bool
		wantErr   error
		wantFlags uint32
		wantBound uint64
		timeoutIn time.Duration
	}{
		{name: "already signaled", status: AlreadySignaled, wantOK: true, wantBound: 500_000, timeoutIn: 500 * time.Microsecond},
		{name: "condition satisfied", status: ConditionSatisfied, wantOK: true, wantBound: 500_000, timeoutIn: 500 * time.Microsecond},
		{name: "timeout", status: TimeoutExpired, wantErr: ErrFenceTimeout, wantBound: 500_000, timeoutIn: 500 * time.Microsecond},
		{name: "wait failed", status: WaitFailed, wantErr: ErrFenceWaitFailed, wantBound: 500_000, timeoutIn: 500 * time.Microsecond},
		{name: "zero bound", status: ConditionSatisfied, wantOK: true, wantBound: 0, timeoutIn: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var gotFlags uint32
			var gotBound uint64
			loader := &Loader{
				fenceSync: func(uint32, uint32) uintptr { return 1 },
				clientWaitSync: func(_ uintptr, flags uint32, bound uint64) uint32 {
					gotFlags, gotBound = flags, bound
					return testCase.status
				},
				deleteSync: func(uintptr) {},
			}
			ok, err := loader.WaitFence(1, testCase.timeoutIn)
			if ok != testCase.wantOK {
				t.Fatalf("ok = %t, want %t", ok, testCase.wantOK)
			}
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("err = %v, want %v", err, testCase.wantErr)
			}
			if gotFlags != SyncFlushCommandsBit {
				t.Fatalf("flags = %#x, want SyncFlushCommandsBit: without the flush bit the wait can return immediately with a stale result", gotFlags)
			}
			if gotBound != testCase.wantBound {
				t.Fatalf("bound = %d ns, want %d", gotBound, testCase.wantBound)
			}
		})
	}
}

func TestFenceConstants(t *testing.T) {
	checks := map[string]uint32{
		"SyncGpuCommandsComplete": SyncGpuCommandsComplete,
		"SyncFlushCommandsBit":    SyncFlushCommandsBit,
		"AlreadySignaled":         AlreadySignaled,
		"TimeoutExpired":          TimeoutExpired,
		"ConditionSatisfied":      ConditionSatisfied,
		"WaitFailed":              WaitFailed,
	}
	want := map[string]uint32{
		"SyncGpuCommandsComplete": 0x9117,
		"SyncFlushCommandsBit":    0x0001,
		"AlreadySignaled":         0x911A,
		"TimeoutExpired":          0x911B,
		"ConditionSatisfied":      0x911C,
		"WaitFailed":              0x911D,
	}
	for name, value := range checks {
		if value != want[name] {
			t.Fatalf("%s = %#x, want %#x", name, value, want[name])
		}
	}
}
