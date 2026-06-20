package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"github.com/bnema/purego-cef2gtk/internal/dmabuf"
	"github.com/bnema/purego-cef2gtk/internal/egl"
	"github.com/bnema/purego-cef2gtk/internal/gl"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
	"github.com/bnema/puregotk/v4/gdk"
	"github.com/bnema/puregotk/v4/gtk"
)

const (
	texWidth  = 4
	texHeight = 4
)

type probeOutput struct {
	Status string                    `json:"status"`
	Error  string                    `json:"error,omitempty"`
	Probe  *probeResult              `json:"probe,omitempty"`
	ProbeC *gtkgl.ContextProbeResult `json:"context_probe,omitempty"`
}

type probeResult struct {
	EGLImporterCreated bool `json:"egl_importer_created"`
	GLBackendCreated   bool `json:"gl_backend_created"`
	CopierCreated      bool `json:"copier_created"`
	DMABUFAllocated    bool `json:"dmabuf_allocated"`
	DMABUFImported     bool `json:"dmabuf_imported"`
	CopyPipelineValid  bool `json:"copy_pipeline_valid"`
	DrawPipelineValid  bool `json:"draw_pipeline_valid"`
	ReadbackMatch      bool `json:"readback_match"`
}

// knownRGBA returns a 4×4 RGBA pattern where every pixel has a unique RGBA
// value so that readback can be verified unambiguously.
func knownRGBA() []byte {
	return []byte{
		255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255, 255, 255, 255, 255,
		255, 255, 0, 255, 255, 0, 255, 255, 0, 255, 255, 255, 128, 128, 128, 255,
		192, 64, 32, 255, 64, 192, 128, 255, 32, 128, 192, 255, 16, 32, 64, 255,
		255, 128, 0, 255, 0, 128, 255, 255, 128, 0, 128, 255, 64, 64, 64, 255,
	}
}

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if os.Getenv("WAYLAND_DISPLAY") == "" {
		writeJSON(probeOutput{Status: "skipped", Error: "WAYLAND_DISPLAY is not set; DMABUF import-copy probe not run"})
		os.Exit(2)
	}
	if !gtk.InitCheck() {
		writeJSON(probeOutput{Status: "skipped", Error: "gtk.InitCheck failed; no usable GTK display"})
		os.Exit(2)
	}

	area := gtk.NewGLArea()
	if area == nil {
		writeJSON(probeOutput{Status: "error", Error: "gtk.NewGLArea returned nil"})
		os.Exit(1)
	}
	area.SetAllowedApis(gdk.GlApiGlValue | gdk.GlApiGlesValue)
	area.SetSizeRequest(4, 4)

	win := gtk.NewWindow()
	if win == nil {
		writeJSON(probeOutput{Status: "error", Error: "gtk.NewWindow returned nil"})
		os.Exit(1)
	}
	win.SetChild(&area.Widget)
	win.Realize()
	area.Realize()

	output := runProbe(area)
	win.Destroy()

	if output.Status != "ok" {
		writeJSON(output)
		if output.Status == "skipped" || output.Status == "unsupported" {
			os.Exit(2)
		}
		os.Exit(1)
	}
	writeJSON(output)
}

type probePipeline struct {
	importer *egl.Importer
	gl       *gl.Loader
	copier   *gl.TexturedQuadCopier
}

func runProbe(area *gtk.GLArea) probeOutput {
	ctxProbe, err := gtkgl.ProbeCurrentGLAreaContext(area)
	allowSyntheticFallback := false
	if err != nil {
		if errors.Is(err, gtkgl.ErrNonWaylandBackend) {
			return probeOutput{Status: "unsupported", Error: fmt.Sprintf("context probe: %v", err), Probe: &probeResult{}, ProbeC: &ctxProbe}
		}
		if !errors.Is(err, gtkgl.ErrMissingDMABUFImport) {
			return probeOutput{Status: "error", Error: fmt.Sprintf("context probe: %v", err), Probe: &probeResult{}, ProbeC: &ctxProbe}
		}
		// This probe can still validate the accelerated copy/draw/readback path
		// with a synthetic GL texture when the current EGL display lacks DMABUF
		// import support. Real DMABUF import is reported separately via the result
		// booleans below.
		allowSyntheticFallback = true
	}

	result := &probeResult{}
	pipeline, err := newProbePipeline(ctxProbe.GLAPI, result, allowSyntheticFallback)
	if err != nil {
		return probeOutput{Status: "error", Error: err.Error(), Probe: result, ProbeC: &ctxProbe}
	}
	defer pipeline.close()

	pixels := knownRGBA()
	size := dmabuf.Size{Width: texWidth, Height: texHeight}
	srcTex, dmabufUsed := pipeline.sourceTexture(pixels, size, allowSyntheticFallback)
	if srcTex == 0 {
		return probeOutput{
			Status: "error",
			Error:  "failed to create source texture (DMABUF import and synthetic fallback both failed)",
			Probe:  result,
			ProbeC: &ctxProbe,
		}
	}
	defer pipeline.deleteTexture(srcTex)
	result.DMABUFAllocated = dmabufUsed
	result.DMABUFImported = dmabufUsed

	owned, err := pipeline.copyAndVerify(srcTex, size, pixels, result)
	if err != nil {
		return probeOutput{Status: "error", Error: err.Error(), Probe: result, ProbeC: &ctxProbe}
	}
	defer pipeline.deleteTexture(uint32(owned))

	if err := pipeline.copier.DrawTextureToCurrentFramebuffer(owned, size); err != nil {
		return probeOutput{Status: "error", Error: fmt.Sprintf("DrawTextureToCurrentFramebuffer: %v", err), Probe: result, ProbeC: &ctxProbe}
	}
	result.DrawPipelineValid = true

	return probeOutput{Status: "ok", Probe: result, ProbeC: &ctxProbe}
}

func newProbePipeline(glAPI string, result *probeResult, allowMissingImporter bool) (*probePipeline, error) {
	glBackend, err := gl.NewBackendFromCurrentContext()
	if err != nil {
		return nil, fmt.Errorf("GL backend: %w", err)
	}
	result.GLBackendCreated = true

	copier, err := gl.NewTexturedQuadCopierForAPI(glBackend, glAPI)
	if err != nil {
		glBackend.Close()
		return nil, fmt.Errorf("copier: %w", err)
	}
	result.CopierCreated = true

	pipeline := &probePipeline{gl: glBackend, copier: copier}
	importer, err := egl.NewImporterFromCurrentDisplay()
	if err != nil {
		if allowMissingImporter {
			return pipeline, nil
		}
		pipeline.close()
		return nil, fmt.Errorf("EGL importer: %w", err)
	}
	pipeline.importer = importer
	result.EGLImporterCreated = true
	return pipeline, nil
}

func (p *probePipeline) close() {
	if p.importer != nil {
		p.importer.Close()
	}
	if p.copier != nil {
		p.copier.Close()
	}
	if p.gl != nil {
		p.gl.Close()
	}
}

func (p *probePipeline) sourceTexture(pixels []byte, size dmabuf.Size, allowSyntheticFallback bool) (uint32, bool) {
	if p.importer != nil {
		if tex, ok := importDMABUFSource(p.importer, p.gl, pixels, size); ok {
			return tex, true
		}
		if !allowSyntheticFallback {
			return 0, false
		}
	}
	if !allowSyntheticFallback {
		return 0, false
	}
	return createSyntheticTexture(p.gl, pixels, size.Width, size.Height), false
}

func (p *probePipeline) copyAndVerify(srcTex uint32, size dmabuf.Size, pixels []byte, result *probeResult) (gl.Texture, error) {
	owned, err := p.copier.CopyImportedToOwned(gl.Texture(srcTex), size, 0)
	if err != nil {
		return 0, fmt.Errorf("CopyImportedToOwned: %w", err)
	}
	result.CopyPipelineValid = true

	if !verifyCopyResult(p.gl, uint32(owned), size, pixels) {
		p.deleteTexture(uint32(owned))
		return 0, errors.New("readback verification failed")
	}
	result.ReadbackMatch = true
	return owned, nil
}

func (p *probePipeline) deleteTexture(tex uint32) {
	if tex != 0 {
		p.gl.DeleteTextures(1, &tex)
	}
}

// importDMABUFSource allocates a real DMABUF, fills it with known pixel data,
// imports it via EGL, and creates a GL texture. Returns (texture, true) on
// success, or (0, false) if DMABUF allocation or import fails.
func importDMABUFSource(imp *egl.Importer, glBackend *gl.Loader, pixels []byte, size dmabuf.Size) (tex uint32, ok bool) {
	dmaBufFD, pitch, bufSize, drmFD, err := allocateDMABUF(int32(size.Width), int32(size.Height), 32, pixels)
	if err != nil {
		return 0, false
	}

	// Cleanup both FDs on failure; on success we close them after GL adoption.
	defer func() {
		if !ok {
			releaseDMABUF(dmaBufFD, drmFD)
		}
	}()

	// Build a BorrowedFrame wrapping this dma-buf FD.
	// DRM format ABGR8888 on little-endian x86 has byte order R[0], G[1], B[2],
	// A[3], matching our RGBA pixel data exactly.
	frame := dmabuf.BorrowedFrame{
		CodedSize: size,
		Format:    dmabuf.FormatABGR8888,
		Modifier:  dmabuf.ModifierLinear,
		Planes: []dmabuf.Plane{{
			FD:     dmaBufFD,
			Stride: pitch,
			Offset: 0,
			Size:   uint64(bufSize),
		}},
	}

	// Import via EGL.
	eglImage, err := imp.ImportDMABUF(frame)
	if err != nil {
		return 0, false
	}

	// Create GL texture from the EGLImage.
	glTex, err := glBackend.ImportEGLImageToTexture(uintptr(eglImage))
	if err != nil {
		_ = imp.Destroy(eglImage)
		return 0, false
	}
	tex = uint32(glTex)

	// Destroy the EGLImage — the GL texture now owns the backing memory.
	if err := imp.Destroy(eglImage); err != nil {
		glBackend.DeleteTextures(1, &tex)
		return 0, false
	}

	// Close both FDs; the GL texture + driver keep a reference to the buffer.
	releaseDMABUF(dmaBufFD, drmFD)

	return tex, true
}

func createSyntheticTexture(glBackend *gl.Loader, pixels []byte, width, height int32) uint32 {
	var tex uint32
	glBackend.GenTextures(1, &tex)
	glBackend.BindTexture(gl.Texture2D, tex)
	glBackend.TexParameteri(gl.Texture2D, gl.TextureMinFilter, int32(gl.Linear))
	glBackend.TexParameteri(gl.Texture2D, gl.TextureMagFilter, int32(gl.Linear))
	glBackend.TexParameteri(gl.Texture2D, gl.TextureWrapS, int32(gl.ClampToEdge))
	glBackend.TexParameteri(gl.Texture2D, gl.TextureWrapT, int32(gl.ClampToEdge))
	glBackend.TexImage2D(gl.Texture2D, 0, int32(gl.RGBA), width, height, 0, gl.RGBA, gl.UnsignedByte, unsafe.Pointer(&pixels[0]))
	if err := gl.CheckError(glBackend, "synthetic texture create"); err != nil {
		glBackend.DeleteTextures(1, &tex)
		return 0
	}
	return tex
}

// verifyCopyResult reads back the owned texture through an FBO and checks for
// an exact RGBA match. It accepts either top-first or bottom-first row order so
// the probe stays independent of GL readback orientation while still verifying
// pixel multiplicity and positions within each row.
func verifyCopyResult(glBackend *gl.Loader, ownedTex uint32, size dmabuf.Size, expected []byte) bool {
	const rgbaSize = 4

	var fbo uint32
	glBackend.GenFramebuffers(1, &fbo)
	defer glBackend.DeleteFramebuffers(1, &fbo)
	glBackend.BindFramebuffer(gl.Framebuffer, fbo)
	defer glBackend.BindFramebuffer(gl.Framebuffer, 0)
	glBackend.FramebufferTexture2D(gl.Framebuffer, gl.ColorAttachment0, gl.Texture2D, ownedTex, 0)
	if status := glBackend.CheckFramebufferStatus(gl.Framebuffer); status != gl.FramebufferComplete {
		return false
	}

	readback := make([]byte, size.Width*size.Height*rgbaSize)
	glBackend.ReadPixels(0, 0, size.Width, size.Height, gl.RGBA, gl.UnsignedByte, unsafe.Pointer(&readback[0]))
	if err := gl.CheckError(glBackend, "readback copy result"); err != nil {
		return false
	}

	return rgbaEqual(readback, expected) || rgbaEqual(readback, flipRows(expected, int(size.Width), int(size.Height)))
}

func rgbaEqual(got, want []byte) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func flipRows(src []byte, width, height int) []byte {
	const rgbaSize = 4
	rowSize := width * rgbaSize
	dst := make([]byte, len(src))
	for y := 0; y < height; y++ {
		copy(dst[y*rowSize:(y+1)*rowSize], src[(height-1-y)*rowSize:(height-y)*rowSize])
	}
	return dst
}

func writeJSON(v probeOutput) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal probe output: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}
