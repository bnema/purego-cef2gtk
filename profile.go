package cef2gtk

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	internalprofile "github.com/bnema/purego-cef2gtk/internal/profile"
)

const defaultProfileInterval = time.Second

// DurationStats aggregates duration samples for one profiling window.
type DurationStats = internalprofile.DurationStats

// GCStats summarizes Go runtime GC state for one profiling window.
type GCStats = internalprofile.GCStats

// GDKPipelineSnapshot reports one bounded window of GDK present-pipeline data.
//
// The four duration series are wall-clock elapsed times on one Go monotonic
// clock, not CPU execution times. Quantiles are nearest-rank values over the
// retained samples of that window and are labelled `sampled` in the JSON;
// missing data is absent rather than zero.
type GDKPipelineSnapshot = internalprofile.GDKPipelineSnapshot

// FrameTimelineSchemaVersion versions the public `gdk_pipeline` object.
const FrameTimelineSchemaVersion = internalprofile.FrameTimelineSchemaVersion

// ProfileSnapshot contains one profiling window of render-pipeline metrics.
type ProfileSnapshot = internalprofile.Snapshot

// ProfileOptions configures development-only render profiling for a View.
type ProfileOptions struct {
	Enabled    bool
	Interval   time.Duration
	OnSnapshot func(ProfileSnapshot)
	Writer     io.Writer
}

func (opts ProfileOptions) normalizedInterval() time.Duration {
	if opts.Interval <= 0 {
		return defaultProfileInterval
	}
	return opts.Interval
}

func writeProfileSnapshot(w io.Writer, snap ProfileSnapshot) error {
	if w == nil {
		return nil
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write cef2gtk profile snapshot: %w", err)
	}
	return nil
}
