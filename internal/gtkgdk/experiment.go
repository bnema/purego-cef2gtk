package gtkgdk

import (
	"os"
	"strconv"
	"strings"
)

// Render-path knobs for the GDK DMA-BUF presenter.
//
// They exist so a single build can be compared against the previous behaviour
// without a rebuild: every knob defaults to the experimental value and has an
// explicit opt-out. None of them is part of the supported configuration
// surface, and none of them establishes buffer ownership.
const (
	// GraphicsOffloadEnvVar wraps the presenter picture in GtkGraphicsOffload to
	// ask the compositor to consume the presented DMA-BUF instead of GSK
	// compositing the picture contents. GTK may still fall back to compositing
	// (clipping, transforms, formats), so this is a request, not a guarantee of
	// direct scanout. Set to "1"/"true"/"yes"/"on" to enable it.
	GraphicsOffloadEnvVar = "PUREGO_CEF2GTK_GDK_GRAPHICS_OFFLOAD"
	// ImportPriorityEnvVar selects the GLib priority of the GTK-thread frame
	// import. "default" runs it alongside ordinary main-loop work; "idle"
	// restores the previous G_PRIORITY_DEFAULT_IDLE behaviour.
	ImportPriorityEnvVar = "PUREGO_CEF2GTK_GDK_IMPORT_PRIORITY"
	// RetiredTexturesEnvVar bounds how many superseded textures stay referenced
	// after the presenter moved to a newer one. Accepted range 1..16.
	RetiredTexturesEnvVar = "PUREGO_CEF2GTK_GDK_RETIRED_TEXTURES"
)

// GLib source priorities. g_idle_add() and g_idle_add_once() schedule at
// G_PRIORITY_DEFAULT_IDLE, which runs after ordinary priority-0 main-loop work.
const (
	glibPriorityDefault     = 0
	glibPriorityDefaultIdle = 200
)

// GraphicsOffloadEnabled reports whether the presenter should hand its texture
// to the compositor through GtkGraphicsOffload. The wrapper is only installed
// when the loaded GTK exposes the widget (4.14+). Default: disabled.
func GraphicsOffloadEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(GraphicsOffloadEnvVar))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// importPriority returns the GLib priority for the scheduled frame import.
func importPriority() int {
	if strings.ToLower(strings.TrimSpace(os.Getenv(ImportPriorityEnvVar))) == "idle" {
		return glibPriorityDefaultIdle
	}
	return glibPriorityDefault
}

// retiredTextureLimitFromEnv returns how many superseded textures the presenter
// keeps referenced. Empty and unparsable values fall back to
// defaultRetiredTextureLimit; values above the compile-time ring size clamp to
// the ring size rather than falling back, and values below 1 fall back.
func retiredTextureLimitFromEnv() int {
	raw := strings.TrimSpace(os.Getenv(RetiredTexturesEnvVar))
	if raw == "" {
		return defaultRetiredTextureLimit
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return defaultRetiredTextureLimit
	}
	if limit > retiredTextureStorage {
		return retiredTextureStorage
	}
	return limit
}
