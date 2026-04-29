package cefadapter

import "github.com/bnema/purego-cef/cef"

const ozonePlatformSwitch = "ozone-platform"

// ConfigureWaylandGPUCommandLine configures CEF command-line switches required
// for Wayland accelerated rendering without adding software fallbacks.
func ConfigureWaylandGPUCommandLine(commandLine cef.CommandLine) {
	if commandLine == nil || commandLine.HasSwitch(ozonePlatformSwitch) {
		return
	}
	commandLine.AppendSwitchWithValue(ozonePlatformSwitch, "wayland")
}
