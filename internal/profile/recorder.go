package profile

import (
	"encoding/json"
	"runtime"
	"sync"
	"time"
)

// DurationStats aggregates duration samples for a profiling window.
type DurationStats struct {
	Count uint64        `json:"count"`
	Total time.Duration `json:"-"`
	Min   time.Duration `json:"-"`
	Max   time.Duration `json:"-"`
}

func (s *DurationStats) Add(d time.Duration) {
	if d < 0 {
		return
	}
	s.Count++
	s.Total += d
	if s.Min == 0 || d < s.Min {
		s.Min = d
	}
	if d > s.Max {
		s.Max = d
	}
}

func (s DurationStats) Avg() time.Duration {
	if s.Count == 0 {
		return 0
	}
	return time.Duration(int64(s.Total) / int64(s.Count))
}

func (s DurationStats) MarshalJSON() ([]byte, error) {
	type durationStatsJSON struct {
		Count   uint64  `json:"count"`
		TotalMS float64 `json:"total_ms"`
		AvgMS   float64 `json:"avg_ms"`
		MinMS   float64 `json:"min_ms"`
		MaxMS   float64 `json:"max_ms"`
	}
	return json.Marshal(durationStatsJSON{
		Count:   s.Count,
		TotalMS: durationMS(s.Total),
		AvgMS:   durationMS(s.Avg()),
		MinMS:   durationMS(s.Min),
		MaxMS:   durationMS(s.Max),
	})
}

func durationMS(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// GCStats summarizes Go runtime GC state for a profiling window.
type GCStats struct {
	NumGC        uint32 `json:"num_gc"`
	NumGCDelta   uint32 `json:"num_gc_delta"`
	PauseTotalNS uint64 `json:"pause_total_ns"`
	PauseDeltaNS uint64 `json:"pause_delta_ns"`
	LastPauseNS  uint64 `json:"last_pause_ns"`
	HeapAlloc    uint64 `json:"heap_alloc"`
	HeapSys      uint64 `json:"heap_sys"`
	NextGC       uint64 `json:"next_gc"`
	NumGoroutine int    `json:"num_goroutine"`
}

// Snapshot contains one profiling window of render-pipeline metrics.
type Snapshot struct {
	Time   time.Time     `json:"time"`
	Window time.Duration `json:"-"`

	FramesReceived    uint64 `json:"frames_received"`
	FramesQueued      uint64 `json:"frames_queued"`
	FramesRendered    uint64 `json:"frames_rendered"`
	ImportFailures    uint64 `json:"import_failures"`
	RenderFailures    uint64 `json:"render_failures"`
	UnsupportedPaints uint64 `json:"unsupported_paints"`

	GTKWaitCPU    DurationStats `json:"gtk_wait_cpu"`
	ImportCopyCPU DurationStats `json:"import_copy_cpu"`
	ImportCPU     DurationStats `json:"import_cpu"`
	CopyCPU       DurationStats `json:"copy_cpu"`
	RenderCPU     DurationStats `json:"render_cpu"`
	CopyGPU       DurationStats `json:"copy_gpu"`
	DrawGPU       DurationStats `json:"draw_gpu"`

	GC GCStats `json:"gc"`
}

func (s Snapshot) MarshalJSON() ([]byte, error) {
	type snapshotJSON struct {
		Time              time.Time     `json:"time"`
		WindowMS          float64       `json:"window_ms"`
		FramesReceived    uint64        `json:"frames_received"`
		FramesQueued      uint64        `json:"frames_queued"`
		FramesRendered    uint64        `json:"frames_rendered"`
		ImportFailures    uint64        `json:"import_failures"`
		RenderFailures    uint64        `json:"render_failures"`
		UnsupportedPaints uint64        `json:"unsupported_paints"`
		GTKWaitCPU        DurationStats `json:"gtk_wait_cpu"`
		ImportCopyCPU     DurationStats `json:"import_copy_cpu"`
		ImportCPU         DurationStats `json:"import_cpu"`
		CopyCPU           DurationStats `json:"copy_cpu"`
		RenderCPU         DurationStats `json:"render_cpu"`
		CopyGPU           DurationStats `json:"copy_gpu"`
		DrawGPU           DurationStats `json:"draw_gpu"`
		GC                GCStats       `json:"gc"`
	}
	return json.Marshal(snapshotJSON{
		Time:              s.Time,
		WindowMS:          durationMS(s.Window),
		FramesReceived:    s.FramesReceived,
		FramesQueued:      s.FramesQueued,
		FramesRendered:    s.FramesRendered,
		ImportFailures:    s.ImportFailures,
		RenderFailures:    s.RenderFailures,
		UnsupportedPaints: s.UnsupportedPaints,
		GTKWaitCPU:        s.GTKWaitCPU,
		ImportCopyCPU:     s.ImportCopyCPU,
		ImportCPU:         s.ImportCPU,
		CopyCPU:           s.CopyCPU,
		RenderCPU:         s.RenderCPU,
		CopyGPU:           s.CopyGPU,
		DrawGPU:           s.DrawGPU,
		GC:                s.GC,
	})
}

// Recorder aggregates render profiling metrics. The zero value is usable after Start.
type Recorder struct {
	mu             sync.Mutex
	started        bool
	windowStart    time.Time
	lastGCNum      uint32
	lastPauseTotal uint64
	current        Snapshot
}

func NewRecorder() *Recorder { return &Recorder{} }

func (r *Recorder) Start(now time.Time) {
	if r == nil {
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	r.mu.Lock()
	r.started = true
	r.windowStart = now
	r.lastGCNum = ms.NumGC
	r.lastPauseTotal = ms.PauseTotalNs
	r.current = Snapshot{}
	r.mu.Unlock()
}

func (r *Recorder) RecordFrameReceived()          { r.add(func(s *Snapshot) { s.FramesReceived++ }) }
func (r *Recorder) RecordFrameQueued()            { r.add(func(s *Snapshot) { s.FramesQueued++ }) }
func (r *Recorder) RecordFrameRendered()          { r.add(func(s *Snapshot) { s.FramesRendered++ }) }
func (r *Recorder) RecordImportFailure()          { r.add(func(s *Snapshot) { s.ImportFailures++ }) }
func (r *Recorder) RecordRenderFailure()          { r.add(func(s *Snapshot) { s.RenderFailures++ }) }
func (r *Recorder) RecordUnsupportedPaint()       { r.add(func(s *Snapshot) { s.UnsupportedPaints++ }) }
func (r *Recorder) RecordGTKWait(d time.Duration) { r.add(func(s *Snapshot) { s.GTKWaitCPU.Add(d) }) }
func (r *Recorder) RecordImportCopyCPU(d time.Duration) {
	r.add(func(s *Snapshot) { s.ImportCopyCPU.Add(d) })
}
func (r *Recorder) RecordImportCPU(d time.Duration) { r.add(func(s *Snapshot) { s.ImportCPU.Add(d) }) }
func (r *Recorder) RecordCopyCPU(d time.Duration)   { r.add(func(s *Snapshot) { s.CopyCPU.Add(d) }) }
func (r *Recorder) RecordRenderCPU(d time.Duration) { r.add(func(s *Snapshot) { s.RenderCPU.Add(d) }) }
func (r *Recorder) RecordCopyGPU(d time.Duration)   { r.add(func(s *Snapshot) { s.CopyGPU.Add(d) }) }
func (r *Recorder) RecordDrawGPU(d time.Duration)   { r.add(func(s *Snapshot) { s.DrawGPU.Add(d) }) }

func (r *Recorder) add(fn func(*Snapshot)) {
	if r == nil || fn == nil {
		return
	}
	r.mu.Lock()
	fn(&r.current)
	r.mu.Unlock()
}

func (r *Recorder) MaybeSnapshot(now time.Time, interval time.Duration) (Snapshot, bool) {
	if r == nil || interval <= 0 {
		return Snapshot{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		r.started = true
		r.windowStart = now
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		r.lastGCNum = ms.NumGC
		r.lastPauseTotal = ms.PauseTotalNs
		return Snapshot{}, false
	}
	if now.Sub(r.windowStart) < interval {
		return Snapshot{}, false
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	snap := r.current
	snap.Time = now
	snap.Window = now.Sub(r.windowStart)
	snap.GC = GCStats{
		NumGC:        ms.NumGC,
		NumGCDelta:   ms.NumGC - r.lastGCNum,
		PauseTotalNS: ms.PauseTotalNs,
		PauseDeltaNS: ms.PauseTotalNs - r.lastPauseTotal,
		LastPauseNS:  lastPauseNS(ms),
		HeapAlloc:    ms.HeapAlloc,
		HeapSys:      ms.HeapSys,
		NextGC:       ms.NextGC,
		NumGoroutine: runtime.NumGoroutine(),
	}
	r.current = Snapshot{}
	r.windowStart = now
	r.lastGCNum = ms.NumGC
	r.lastPauseTotal = ms.PauseTotalNs
	return snap, true
}

func lastPauseNS(ms runtime.MemStats) uint64 {
	if ms.NumGC == 0 {
		return 0
	}
	idx := (ms.NumGC + 255) % 256
	return ms.PauseNs[idx]
}
