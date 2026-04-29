# Dumber migration notes

`purego-cef2gtk` owns the reusable GTK4 + CEF accelerated OSR bridge:

- Wayland/GPU command-line configuration (`ConfigureCommandLine`)
- accelerated windowless `cef.WindowInfo` setup (`ConfigureWindowInfo`)
- `GtkGLArea` view lifecycle and render handler adapter
- GTK input forwarding to `cef.BrowserHost`
- diagnostics for accelerated paint/import/render failures

Dumber remains responsible for application-specific behavior:

- browser/tab/window orchestration and navigation state
- logging policy and user-visible error handling
- clipboard policy beyond basic paste injection
- explicit copy/cut routing and selection tracking
- middle-click/link handling
- app shortcuts, context menus, downloads, schemes, and persistence

Candidate Dumber files to replace or shrink after adopting this package:

- `internal/infrastructure/cef/gl_loader.go`
- `internal/infrastructure/cef/render_pipeline.go`
- `internal/infrastructure/cef/input_bridge.go`
- `internal/infrastructure/cef/factory.go`
- `internal/infrastructure/cef/resize_reconciler.go`

Migration should be handled in a separate Dumber-focused plan. This package intentionally does not carry Dumber-specific imports, logging, tab semantics, or browser factory policy.
