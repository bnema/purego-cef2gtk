package cefadapter

import (
	"os"
	"strings"

	"github.com/bnema/purego-cef/cef"
)

const (
	ozonePlatformSwitch  = "ozone-platform"
	useAngleSwitch       = "use-angle"
	useGLSwitch          = "use-gl"
	enableFeaturesSwitch = "enable-features"

	angleBackendEnvVar = "PUREGO_CEF2GTK_ANGLE_BACKEND"
)

// ConfigureWaylandGPUCommandLine configures CEF command-line switches required
// for Wayland accelerated rendering without adding software fallbacks.
func ConfigureWaylandGPUCommandLine(commandLine cef.CommandLine) {
	if commandLine == nil {
		return
	}
	angleBackend, angleBackendExplicit := angleBackendFromEnv()
	if angleBackendExplicit {
		commandLine.RemoveSwitch(useAngleSwitch)
		commandLine.AppendSwitchWithValue(useAngleSwitch, angleBackend)
	} else if !commandLine.HasSwitch(useAngleSwitch) {
		commandLine.AppendSwitchWithValue(useAngleSwitch, angleBackend)
	}

	effectiveAngleBackend := normalizeExistingAngleBackend(commandLine.GetSwitchValue(useAngleSwitch))
	if effectiveAngleBackend == "none" {
		commandLine.RemoveSwitch(useGLSwitch)
	} else {
		if !commandLine.HasSwitch(useGLSwitch) {
			commandLine.AppendSwitchWithValue(useGLSwitch, "angle")
		}
		if effectiveAngleBackend == "vulkan" {
			features := mergeCommaTokens(
				commandLine.GetSwitchValue(enableFeaturesSwitch),
				"Vulkan,DefaultANGLEVulkan,VulkanFromANGLE",
			)
			commandLine.RemoveSwitch(enableFeaturesSwitch)
			commandLine.AppendSwitchWithValue(enableFeaturesSwitch, features)
		}
	}
	if !commandLine.HasSwitch(ozonePlatformSwitch) {
		commandLine.AppendSwitchWithValue(ozonePlatformSwitch, "wayland")
	}
}

func angleBackendFromEnv() (backend string, explicit bool) {
	value, explicit := os.LookupEnv(angleBackendEnvVar)
	return normalizeAngleBackend(value), explicit
}

func normalizeAngleBackend(value string) string {
	normalized := normalizeExistingAngleBackend(value)
	switch normalized {
	case "vulkan", "none":
		return normalized
	default:
		return "gl-egl"
	}
}

func normalizeExistingAngleBackend(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func mergeCommaTokens(existing, required string) string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, list := range []string{existing, required} {
		for _, token := range strings.Split(list, ",") {
			token = strings.TrimSpace(token)
			if token == "" || seen[token] {
				continue
			}
			seen[token] = true
			out = append(out, token)
		}
	}
	return strings.Join(out, ",")
}
