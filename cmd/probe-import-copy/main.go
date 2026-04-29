package main

import (
	"fmt"
	"os"
)

// TODO(probe-import-copy, main): main intentionally exits with SKIP (exit 77) because no real
// CEF DMABUF sample input wiring exists yet. Replace with actual probing logic when available.
func main() {
	fmt.Fprintln(os.Stderr, "SKIP: probe-import-copy requires a real CEF DMABUF sample; no sample input is wired yet")
	os.Exit(77)
}
