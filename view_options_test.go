package cef2gtk

import "testing"

func TestResolveViewOptionsPreservesEnvAutoBackcompat(t *testing.T) {
	t.Setenv(backendEnvVar, "auto")

	got, err := resolveViewOptions(ViewOptions{})
	if err != nil {
		t.Fatalf("resolveViewOptions() error = %v", err)
	}
	if got.Backend != BackendAuto {
		t.Fatalf("Backend = %q, want %q", got.Backend, BackendAuto)
	}
}

func TestResolveViewOptionsDoesNotSplitTypedRenderStackWithBackendEnv(t *testing.T) {
	t.Setenv(backendEnvVar, "gdk-dmabuf")
	plan, err := ResolveRenderStack(RenderStackEGL)
	if err != nil {
		t.Fatalf("ResolveRenderStack(egl) error = %v", err)
	}

	got, err := resolveViewOptions(ViewOptions{RenderStackPlan: plan})
	if err != nil {
		t.Fatalf("resolveViewOptions() error = %v", err)
	}
	if got.Backend != BackendGLArea {
		t.Fatalf("Backend = %q, want typed EGL backend %q", got.Backend, BackendGLArea)
	}
}
