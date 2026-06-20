package gtkgl

import (
	"errors"
	"testing"

	"github.com/bnema/puregotk/v4/glib"
)

func TestRunOnGTKThreadSyncReturnsErrorOnScheduleFailure(t *testing.T) {
	orig := idleAddOnce
	idleAddOnce = func(*glib.SourceOnceFunc, uintptr) uint {
		return 0 // simulate scheduling failure
	}
	defer func() { idleAddOnce = orig }()

	called := false
	err := RunOnGTKThreadSync(func() {
		called = true
	})
	if !errors.Is(err, ErrIdleScheduleFailed) {
		t.Fatalf("RunOnGTKThreadSync error = %v, want %v", err, ErrIdleScheduleFailed)
	}
	if called {
		t.Fatal("callback was invoked despite scheduling failure")
	}
}

func TestRunOnGTKThreadSyncReturnsNilOnSuccessfulSchedule(t *testing.T) {
	orig := idleAddOnce
	idleAddOnce = func(fn *glib.SourceOnceFunc, data uintptr) uint {
		(*fn)(data) // invoke the callback immediately, simulating GTK main loop
		return 42
	}
	defer func() { idleAddOnce = orig }()

	called := false
	err := RunOnGTKThreadSync(func() {
		called = true
	})
	if err != nil {
		t.Fatalf("RunOnGTKThreadSync error = %v, want nil", err)
	}
	if !called {
		t.Fatal("callback was not executed")
	}
}

func TestRunOnGTKThreadSyncSkipsWhenFnIsNil(t *testing.T) {
	orig := idleAddOnce
	idleAddOnce = func(*glib.SourceOnceFunc, uintptr) uint {
		t.Fatal("idleAddOnce should not be called when fn is nil")
		return 0
	}
	defer func() { idleAddOnce = orig }()

	if err := RunOnGTKThreadSync(nil); err != nil {
		t.Fatalf("RunOnGTKThreadSync(nil) error = %v, want nil", err)
	}
}
