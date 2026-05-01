package cef2gtk

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkgl"
	internalprofile "github.com/bnema/purego-cef2gtk/internal/profile"
)

type profileTestRenderer struct {
	profiler *internalprofile.Recorder
}

func (r *profileTestRenderer) InitializeOnGTKThread() error { return nil }
func (r *profileTestRenderer) ImportAndQueueOnGTKThread(*cef.AcceleratedPaintInfo) (gtkgl.QueuedFrame, error) {
	return gtkgl.QueuedFrame{}, nil
}
func (r *profileTestRenderer) QueueRender()                            {}
func (r *profileTestRenderer) RenderQueuedOnGTKThread() error          { return nil }
func (r *profileTestRenderer) InvalidateOnGTKThread()                  {}
func (r *profileTestRenderer) SetProfiler(p *internalprofile.Recorder) { r.profiler = p }
func (r *profileTestRenderer) Close()                                  {}

func TestViewConfigureProfilingInstallsRecorder(t *testing.T) {
	renderer := &profileTestRenderer{}
	v := &View{backend: BackendGDKDMABUF, renderer: renderer}
	if err := v.ConfigureProfiling(ProfileOptions{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if renderer.profiler == nil {
		t.Fatal("renderer profiler not installed")
	}
	renderer.profiler.MaybeSnapshot(time.Now(), time.Second)
	snap, ok := renderer.profiler.MaybeSnapshot(time.Now().Add(2*time.Second), time.Second)
	if !ok {
		t.Fatal("expected profile snapshot")
	}
	if snap.Backend != "gdk-dmabuf" {
		t.Fatalf("profile backend = %q, want gdk-dmabuf", snap.Backend)
	}
	if err := v.ConfigureProfiling(ProfileOptions{}); err != nil {
		t.Fatal(err)
	}
	if renderer.profiler != nil {
		t.Fatal("renderer profiler not cleared")
	}
}

func TestWriteProfileSnapshotWritesJSONLine(t *testing.T) {
	var buf bytes.Buffer
	snap := ProfileSnapshot{Time: time.Unix(100, 0), Backend: "gdk-dmabuf", FramesReceived: 3, TexturesBuilt: 2, PaintableSwaps: 2}
	if err := writeProfileSnapshot(&buf, snap); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got["frames_received"] != float64(3) || got["backend"] != "gdk-dmabuf" || got["textures_built"] != float64(2) || got["paintable_swaps"] != float64(2) {
		t.Fatalf("json = %s", buf.String())
	}
}
