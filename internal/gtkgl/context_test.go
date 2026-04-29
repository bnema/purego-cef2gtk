package gtkgl

import (
	"errors"
	"testing"

	"github.com/bnema/purego-cef2gtk/internal/egl"
)

func TestContextProbeResultValidateChecksBackendFirst(t *testing.T) {
	r := ContextProbeResult{Backend: BackendX11}
	if err := r.Validate(); !errors.Is(err, ErrNonWaylandBackend) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrNonWaylandBackend)
	}
}

func TestContextProbeResultValidate(t *testing.T) {
	valid := ContextProbeResult{
		Backend:             BackendWayland,
		GLVersion:           "4.6",
		GLVendor:            "vendor",
		GLRenderer:          "renderer",
		EGLDisplay:          "EGLDisplay(0x1)",
		EGLExtensions:       []string{egl.ExtensionDMABUFImport},
		DMABUFImportSupport: true,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*ContextProbeResult)
		want error
	}{
		{
			name: "non wayland",
			edit: func(r *ContextProbeResult) { r.Backend = BackendX11 },
			want: ErrNonWaylandBackend,
		},
		{
			name: "missing display",
			edit: func(r *ContextProbeResult) { r.EGLDisplay = egl.NoDisplay.String() },
			want: ErrMissingEGLDisplay,
		},
		{
			name: "missing dmabuf import",
			edit: func(r *ContextProbeResult) {
				r.DMABUFImportSupport = false
				r.EGLExtensions = nil
			},
			want: ErrMissingDMABUFImport,
		},
		{
			name: "missing gl capability",
			edit: func(r *ContextProbeResult) { r.GLRenderer = "" },
			want: ErrMissingGLAreaCapability,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid
			r.EGLExtensions = append([]string(nil), valid.EGLExtensions...)
			tt.edit(&r)
			if err := r.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.want)
			}
		})
	}
}
