package cef2gtk

import "testing"

func TestOptionsValidateAcceptsDefaultRenderingMode(t *testing.T) {
	if err := (Options{}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestOptionsValidateAcceptsAcceleratedDMABUF(t *testing.T) {
	opts := Options{RenderingMode: RenderingModeAcceleratedDMABUF}
	if err := opts.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestOptionsValidateRejectsUnsupportedRenderingMode(t *testing.T) {
	opts := Options{RenderingMode: RenderingMode("software")}
	if err := opts.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestOptionsNormalizedAppliesDefaultRenderingMode(t *testing.T) {
	got, err := (Options{}).normalized()
	if err != nil {
		t.Fatalf("normalized() error = %v", err)
	}
	if got.RenderingMode != RenderingModeAcceleratedDMABUF {
		t.Fatalf("RenderingMode = %q, want %q", got.RenderingMode, RenderingModeAcceleratedDMABUF)
	}
}

func TestBackendString(t *testing.T) {
	if got := Backend("").String(); got != "auto" {
		t.Fatalf("empty Backend.String() = %q, want auto", got)
	}
	if got := BackendGDKDMABUF.String(); got != "gdk-dmabuf" {
		t.Fatalf("BackendGDKDMABUF.String() = %q", got)
	}
	if got := BackendGLArea.String(); got != "glarea" {
		t.Fatalf("BackendGLArea.String() = %q, want glarea", got)
	}
}

func TestViewOptionsValidateAndNormalizeBackend(t *testing.T) {
	got, err := (ViewOptions{}).normalized()
	if err != nil {
		t.Fatalf("normalized() error = %v", err)
	}
	if got.Backend != BackendGDKDMABUF {
		t.Fatalf("Backend = %q, want %q", got.Backend, BackendGDKDMABUF)
	}

	for _, backend := range []Backend{BackendGDKDMABUF, BackendGLArea} {
		if err := (ViewOptions{Backend: backend}).Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v", backend, err)
		}
	}

	if err := (ViewOptions{Backend: Backend("software")}).Validate(); err == nil {
		t.Fatal("Validate() error = nil, want unsupported backend error")
	}
}

func TestViewOptionsExplicitBackendAutoPreservesFallbackSemantics(t *testing.T) {
	got, err := (ViewOptions{Backend: BackendAuto}).normalized()
	if err != nil {
		t.Fatalf("normalized() error = %v", err)
	}
	if got.Backend != BackendAuto {
		t.Fatalf("Backend = %q, want explicit %q", got.Backend, BackendAuto)
	}
}

func TestViewOptionsRenderStackPlanSelectsBackend(t *testing.T) {
	plan, err := ResolveRenderStack(RenderStackEGL)
	if err != nil {
		t.Fatalf("ResolveRenderStack(egl) error = %v", err)
	}
	got, err := (ViewOptions{RenderStackPlan: plan}).normalized()
	if err != nil {
		t.Fatalf("normalized() error = %v", err)
	}
	if got.Backend != BackendGLArea {
		t.Fatalf("Backend = %q, want %q", got.Backend, BackendGLArea)
	}
}

func TestBackendFromEnv(t *testing.T) {
	t.Setenv(backendEnvVar, " GDK-DMABUF ")
	backend, ok, err := backendFromEnv()
	if err != nil {
		t.Fatalf("backendFromEnv() error = %v", err)
	}
	if !ok || backend != BackendGDKDMABUF {
		t.Fatalf("backendFromEnv() = (%q,%v), want (%q,true)", backend, ok, BackendGDKDMABUF)
	}

	t.Setenv(backendEnvVar, "invalid")
	if _, _, err := backendFromEnv(); err == nil {
		t.Fatal("backendFromEnv() error = nil, want invalid backend error")
	}
}
