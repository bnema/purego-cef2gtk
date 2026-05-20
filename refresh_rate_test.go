package cef2gtk

import "testing"

func TestWindowlessFrameRateForMonitorRefresh(t *testing.T) {
	tests := []struct {
		name           string
		refreshMilliHz int
		opts           RefreshRateOptions
		want           int32
	}{
		{name: "fallback for unknown refresh", refreshMilliHz: 0, opts: RefreshRateOptions{DefaultFPS: 60, MinFPS: 30, MaxFPS: 240}, want: 60},
		{name: "rounds fractional refresh", refreshMilliHz: 59940, opts: RefreshRateOptions{DefaultFPS: 60, MinFPS: 30, MaxFPS: 240}, want: 60},
		{name: "uses high refresh", refreshMilliHz: 144000, opts: RefreshRateOptions{DefaultFPS: 60, MinFPS: 30, MaxFPS: 240}, want: 144},
		{name: "clamps low refresh", refreshMilliHz: 24000, opts: RefreshRateOptions{DefaultFPS: 60, MinFPS: 60, MaxFPS: 240}, want: 60},
		{name: "hard caps extreme refresh", refreshMilliHz: 500000, opts: RefreshRateOptions{DefaultFPS: 60, MinFPS: 30, MaxFPS: 240}, want: 240},
		{name: "normalizes empty options", refreshMilliHz: 165000, opts: RefreshRateOptions{}, want: 165},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WindowlessFrameRateForMonitorRefresh(tt.refreshMilliHz, tt.opts); got != tt.want {
				t.Fatalf("WindowlessFrameRateForMonitorRefresh(%d, %+v) = %d, want %d", tt.refreshMilliHz, tt.opts, got, tt.want)
			}
		})
	}
}
