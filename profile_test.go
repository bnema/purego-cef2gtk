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
func (r *profileTestRenderer) ImportCopyAndQueueOnGTKThread(*cef.AcceleratedPaintInfo) (gtkgl.QueuedFrame, error) {
	return gtkgl.QueuedFrame{}, nil
}
func (r *profileTestRenderer) QueueRender()                            {}
func (r *profileTestRenderer) RenderQueuedOnGTKThread() error          { return nil }
func (r *profileTestRenderer) InvalidateOnGTKThread()                  {}
func (r *profileTestRenderer) SetProfiler(p *internalprofile.Recorder) { r.profiler = p }
func (r *profileTestRenderer) Close()                                  {}

func TestViewConfigureProfilingInstallsRecorder(t *testing.T) {
	renderer := &profileTestRenderer{}
	v := &View{renderer: renderer}
	if err := v.ConfigureProfiling(ProfileOptions{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if renderer.profiler == nil {
		t.Fatal("renderer profiler not installed")
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
	snap := ProfileSnapshot{Time: time.Unix(100, 0), FramesReceived: 3}
	if err := writeProfileSnapshot(&buf, snap); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got["frames_received"] != float64(3) {
		t.Fatalf("json = %s", buf.String())
	}
}
