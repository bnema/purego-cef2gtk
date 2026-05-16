# purego-cef2gtk

GTK4 widget bridge for [`purego-cef`](https://github.com/bnema/purego-cef) accelerated off-screen rendering.

## Status

Early bootstrap. The renderer is Wayland-only and GPU-first:

- CEF accelerated OSR with shared textures/DMABUFs
- GTK4 `GdkDmabufTextureBuilder` + `GtkPicture`/`GtkGraphicsOffload` backend for GTK/GSK rendering
- GTK4 `GtkGLArea` EGL/OpenGL import-and-copy backend as fallback/debug path
- no X11 target
- no `--disable-gpu` or software rendering fallback
- CEF `OnPaint` is treated as a diagnostic/error path, not as a renderer

## Renderer backends

`NewView()` uses `BackendAuto`: it tries `gdk-dmabuf` first and falls back to `glarea` if GDK DMABUF construction is unavailable. Use `NewViewWithOptions` or env overrides for diagnostics:

```go
view := cef2gtk.NewViewWithOptions(cef2gtk.ViewOptions{Backend: cef2gtk.BackendGDKDMABUF})
```

Environment overrides:

```sh
PUREGO_CEF2GTK_BACKEND=auto|gdk-dmabuf|glarea
PUREGO_CEF2GTK_ANGLE_BACKEND=vulkan|gl-egl|none
PUREGO_CEF2GTK_OSR_BACKING_SCALE=auto|on|off
```

`PUREGO_CEF2GTK_OSR_BACKING_SCALE=auto` enables the Linux accelerated-OSR
HiDPI compatibility path only when the GTK surface scale is greater than 1. In
that mode CEF receives a device-sized OSR view rect with a 1x screen scale,
because current CEF shared-texture OSR builds can otherwise report a fractional
`device_scale_factor` while still emitting 1x/logical DMABUF frames. Applications
that call `BrowserHost.SetZoomLevel` should divide their page zoom by
`cef2gtk.OSRBackingScaleFactorForScale(float64(view.DeviceScaleFactor()))` before
converting it to CEF's logarithmic zoom level; this keeps the page's CSS viewport
at the GTK logical size while the OSR backing remains device-sized.

Recommended local checks:

```sh
GSK_RENDERER=vulkan PUREGO_CEF2GTK_BACKEND=gdk-dmabuf go run ./examples/simple-browser
GSK_RENDERER=ngl PUREGO_CEF2GTK_BACKEND=gdk-dmabuf go run ./examples/simple-browser
PUREGO_CEF2GTK_BACKEND=glarea go run ./examples/simple-browser
```

## Usage sketch

```go
// In cef.App.OnBeforeCommandLineProcessing:
cef2gtk.ConfigureCommandLine(commandLine, cef2gtk.CommandLineOptions{})

view := cef2gtk.NewView()
window.SetChild(view.Widget())
window.Present()
view.Widget().Realize()
if err := view.PrepareOnGTKThread(); err != nil {
    return err
}

info := cef.NewWindowInfo()
// After the GTK widget/surface is realized, extract its native platform handle
// with the appropriate GTK/platform API and pass that handle as Parent.
cef2gtk.ConfigureWindowInfo(&info, cef2gtk.WindowInfoOptions{Parent: /* realized native handle */})
settings := cef.NewBrowserSettings()
client := cef.NewClient(myClient{render: view.RenderHandler(cef2gtk.Hooks{})})
cef.BrowserHostCreateBrowser(&info, client, "https://example.com/", &settings, nil, nil)

// In OnAfterCreated(browser cef.Browser), attach the BrowserHost:
_ = view.AttachInput(browser.GetHost(), cef2gtk.InputOptions{Scale: 0})
```

See `examples/simple-browser` for a complete accelerated-only GTK+CEF setup.

## Development

The module depends on the published `github.com/bnema/puregotk` module. During local puregotk development,
you can temporarily add your own `replace github.com/bnema/puregotk => ../puregotk` directive.

```sh
rtk make check
```
