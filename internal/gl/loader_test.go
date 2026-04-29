package gl

import "testing"

func TestLoaderCloseNilSafe(t *testing.T) {
	var loader *Loader
	if err := loader.Close(); err != nil {
		t.Fatalf("Close nil loader returned error: %v", err)
	}
}

func TestConstantsForCopyPath(t *testing.T) {
	checks := map[string]uint32{
		"Texture2D":           Texture2D,
		"Framebuffer":         Framebuffer,
		"ColorAttachment0":    ColorAttachment0,
		"FramebufferComplete": FramebufferComplete,
		"VertexShader":        VertexShader,
		"FragmentShader":      FragmentShader,
		"TriangleStrip":       TriangleStrip,
		"CompileStatus":       CompileStatus,
		"LinkStatus":          LinkStatus,
		"InfoLogLength":       InfoLogLength,
	}
	for name, value := range checks {
		if value == 0 {
			t.Fatalf("%s constant is zero", name)
		}
	}
}
