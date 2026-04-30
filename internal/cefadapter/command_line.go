package cefadapter

import "github.com/bnema/purego-cef/cef"

const (
	ozonePlatformSwitch = "ozone-platform"
	useAngleSwitch      = "use-angle"
)

// ConfigureWaylandGPUCommandLine configures CEF command-line switches required
// for Wayland accelerated rendering without adding software fallbacks.
func ConfigureWaylandGPUCommandLine(commandLine cef.CommandLine) {
	if commandLine == nil {
		return
	}
	// CEF's Linux OSR shared-texture path uses DMABUFs via ANGLE's EGL backend.
	// Avoid Chromium's Vulkan path here: current CEF/Wayland logs warn that
	// --ozone-platform=wayland is not compatible with Vulkan, and Chromium's
	// Linux VAAPI decoder gates GL and Vulkan through different feature paths.
	if !commandLine.HasSwitch(useAngleSwitch) {
		commandLine.AppendSwitchWithValue(useAngleSwitch, "gl-egl")
	}
	if !commandLine.HasSwitch(ozonePlatformSwitch) {
		commandLine.AppendSwitchWithValue(ozonePlatformSwitch, "wayland")
	}
}
