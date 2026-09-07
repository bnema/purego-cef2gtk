package gtkgl

import (
	"fmt"
	"os"
	"sync"
)

// scrollTraceEnv gates the bounded scroll trace. Off by default; enable with
// PUREGO_CEF2GTK_SCROLL_TRACE=1. The trace never logs page content, URLs, or
// personal data — only input geometry, engine state transitions, and
// submission totals. Compare timestamp intervals, not absolute values: the
// monotonic engine clock and the GDK frame clock live in different domains.
const scrollTraceEnv = "PUREGO_CEF2GTK_SCROLL_TRACE"

// scrollTraceMaxLines bounds trace volume so a long session cannot flood
// stderr; one truncation notice follows, then silence.
const scrollTraceMaxLines = 2000

// scrollTracer is a nil-safe bounded stderr sink. A nil *scrollTracer
// disables tracing with a single branch at each call site.
type scrollTracer struct {
	mu        sync.Mutex
	lines     int
	truncated bool
}

func newScrollTracer() *scrollTracer {
	return &scrollTracer{}
}

func scrollTraceEnabled() bool {
	return os.Getenv(scrollTraceEnv) == "1"
}

func (c *scrollController) tracef(format string, args ...any) {
	if c == nil {
		return
	}
	t := c.tracer
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lines >= scrollTraceMaxLines {
		if !t.truncated {
			t.truncated = true
			fmt.Fprintln(os.Stderr, "scroll-trace: truncated at cap")
		}
		return
	}
	t.lines++
	fmt.Fprintf(os.Stderr, "scroll-trace: "+format+"\n", args...)
}
