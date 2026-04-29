package cef2gtk

import (
	"github.com/bnema/purego-cef/cef"

	"github.com/bnema/purego-cef2gtk/internal/cefadapter"
)

// CommandLineOptions configures CEF command-line setup for the GTK bridge.
type CommandLineOptions struct{}

// ConfigureCommandLine configures the CEF command line for Wayland accelerated
// rendering. It is intended to be called from App.OnBeforeCommandLineProcessing.
func ConfigureCommandLine(commandLine cef.CommandLine, _ CommandLineOptions) {
	cefadapter.ConfigureWaylandGPUCommandLine(commandLine)
}
