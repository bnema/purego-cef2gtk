package integration_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeSmokeGate(t *testing.T) {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("WAYLAND_DISPLAY is not set; skipping GTK/EGL runtime smoke test")
	}
	if os.Getenv("PUREGO_CEF2GTK_CEF_RUNTIME_SMOKE") == "" {
		t.Skip("PUREGO_CEF2GTK_CEF_RUNTIME_SMOKE is not set; skipping CEF runtime smoke test")
	}

	root := projectRoot(t)
	runBounded(t, root, 20*time.Second, "GTK/EGL probe", "go", "run", "./cmd/probe-gtk-egl")
	runBounded(t, root, 30*time.Second, "simple browser build", "go", "build", "./examples/simple-browser")
}

func projectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find project root from %s", dir)
		}
		dir = parent
	}
}

func runBounded(t *testing.T, dir string, timeout time.Duration, label string, name string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("%s timed out after %s:\n%s", label, timeout, output.String())
	}
	if err == nil {
		return
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Skipf("%s skipped by command:\n%s", label, output.String())
	}

	t.Fatalf("%s failed: %v\n%s", label, err, output.String())
}
