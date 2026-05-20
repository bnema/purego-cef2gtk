package cef2gtk

import (
	"os"
	"testing"
)

func TestResolveRenderStackDefaultsToVulkan(t *testing.T) {
	plan, err := ResolveRenderStack("")
	if err != nil {
		t.Fatalf("ResolveRenderStack empty error = %v", err)
	}
	assertVulkanRenderStackPlan(t, plan)
}

func TestResolveRenderStackVulkan(t *testing.T) {
	plan, err := ResolveRenderStack(RenderStackVulkan)
	if err != nil {
		t.Fatalf("ResolveRenderStack(vulkan) error = %v", err)
	}
	assertVulkanRenderStackPlan(t, plan)
}

func TestResolveRenderStackEGL(t *testing.T) {
	plan, err := ResolveRenderStack(RenderStackEGL)
	if err != nil {
		t.Fatalf("ResolveRenderStack(egl) error = %v", err)
	}
	if plan.Stack != RenderStackEGL {
		t.Fatalf("Stack = %q, want %q", plan.Stack, RenderStackEGL)
	}
	if plan.Backend != BackendGLArea {
		t.Fatalf("Backend = %q, want %q", plan.Backend, BackendGLArea)
	}
	if plan.ANGLEBackend != "gl-egl" {
		t.Fatalf("ANGLEBackend = %q, want gl-egl", plan.ANGLEBackend)
	}
	if plan.GSKRenderer != "opengl" {
		t.Fatalf("GSKRenderer = %q, want opengl", plan.GSKRenderer)
	}
	if plan.OSRBackingScale != "off" {
		t.Fatalf("OSRBackingScale = %q, want off", plan.OSRBackingScale)
	}
}

func TestResolveRenderStackRejectsUnknownStack(t *testing.T) {
	_, err := ResolveRenderStack(RenderStack("software"))
	if err == nil {
		t.Fatal("ResolveRenderStack(software) error = nil, want unsupported stack error")
	}
}

func TestConfigureRenderStackEnvironmentSetsPlanHints(t *testing.T) {
	plan, err := ResolveRenderStack(RenderStackVulkan)
	if err != nil {
		t.Fatalf("ResolveRenderStack(vulkan) error = %v", err)
	}
	t.Setenv("GSK_RENDERER", "")
	t.Setenv(osrBackingScaleEnvVar, "")

	ConfigureRenderStackEnvironment(plan)

	if got := os.Getenv("GSK_RENDERER"); got != "vulkan" {
		t.Fatalf("GSK_RENDERER = %q, want vulkan", got)
	}
	if got := os.Getenv(osrBackingScaleEnvVar); got != "auto" {
		t.Fatalf("%s = %q, want auto", osrBackingScaleEnvVar, got)
	}
}

func TestConfigureRenderStackEnvironmentSetsEGLBackingScaleOff(t *testing.T) {
	plan, err := ResolveRenderStack(RenderStackEGL)
	if err != nil {
		t.Fatalf("ResolveRenderStack(egl) error = %v", err)
	}
	t.Setenv("GSK_RENDERER", "vulkan")
	t.Setenv(osrBackingScaleEnvVar, "auto")

	ConfigureRenderStackEnvironment(plan)

	if got := os.Getenv("GSK_RENDERER"); got != "opengl" {
		t.Fatalf("GSK_RENDERER = %q, want opengl", got)
	}
	if got := os.Getenv(osrBackingScaleEnvVar); got != "off" {
		t.Fatalf("%s = %q, want off", osrBackingScaleEnvVar, got)
	}
}

func assertVulkanRenderStackPlan(t *testing.T, plan RenderStackPlan) {
	t.Helper()
	if plan.Stack != RenderStackVulkan {
		t.Fatalf("Stack = %q, want %q", plan.Stack, RenderStackVulkan)
	}
	if plan.Backend != BackendGDKDMABUF {
		t.Fatalf("Backend = %q, want %q", plan.Backend, BackendGDKDMABUF)
	}
	if plan.ANGLEBackend != "vulkan" {
		t.Fatalf("ANGLEBackend = %q, want vulkan", plan.ANGLEBackend)
	}
	if plan.GSKRenderer != "vulkan" {
		t.Fatalf("GSKRenderer = %q, want vulkan", plan.GSKRenderer)
	}
	if plan.OSRBackingScale != "auto" {
		t.Fatalf("OSRBackingScale = %q, want auto", plan.OSRBackingScale)
	}
}
