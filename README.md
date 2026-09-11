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

## Renderer stacks

`NewView()` defaults to the Vulkan GPU stack and fails during view setup if the selected stack cannot be constructed. Use `ResolveRenderStack` to choose a coherent stack explicitly:

```go
plan, err := cef2gtk.ResolveRenderStack(cef2gtk.RenderStackVulkan) // or RenderStackEGL
if err != nil {
    return err
}
cef2gtk.ConfigureRenderStackEnvironment(plan) // call before GTK initialization
cef2gtk.ConfigureCommandLine(commandLine, cef2gtk.CommandLineOptions{RenderStackPlan: plan})
view := cef2gtk.NewViewWithOptions(cef2gtk.ViewOptions{RenderStackPlan: plan})
```

Supported stacks:

- `vulkan`: GDK DMABUF presentation with ANGLE Vulkan and GSK Vulkan.
- `egl`: GtkGLArea presentation with ANGLE GL/EGL and GSK OpenGL.

Environment overrides remain available as diagnostics/backcompat escape hatches:

```sh
PUREGO_CEF2GTK_BACKEND=gdk-dmabuf|glarea
PUREGO_CEF2GTK_ANGLE_BACKEND=vulkan|gl-egl|none
PUREGO_CEF2GTK_OSR_BACKING_SCALE=auto|on|off
```

`PUREGO_CEF2GTK_OSR_BACKING_SCALE=auto` enables the Linux accelerated-OSR
HiDPI compatibility path only when the GTK surface scale is greater than 1. In
that mode CEF receives a device-sized OSR view rect with a 1x screen scale,
because current CEF shared-texture OSR builds can otherwise report a fractional
`device_scale_factor` while still emitting 1x/logical DMABUF frames.

Applications that expose page zoom must scale their user-facing zoom by
`(*View).PageZoomCompensation()` (one CSS pixel per logical pixel) before
converting it to CEF's logarithmic zoom level, and divide CEF zoom readback by
the same factor. The method returns 1 when CEF's normal logical OSR contract is
in effect. User zoom itself stays a user-facing value: the compensation changes
with the observed output scale and must not be persisted. Recompute it and
reapply the page zoom from an `AddSizeObserver` callback, which also runs when
the effective scale changes without a logical-size change.

Recommended local checks:

```sh
GSK_RENDERER=vulkan PUREGO_CEF2GTK_BACKEND=gdk-dmabuf go run ./examples/simple-browser
GSK_RENDERER=opengl PUREGO_CEF2GTK_BACKEND=glarea PUREGO_CEF2GTK_ANGLE_BACKEND=gl-egl go run ./examples/simple-browser
```

## Usage sketch

```go
plan, err := cef2gtk.ResolveRenderStack(cef2gtk.RenderStackVulkan)
if err != nil {
    return err
}

cef2gtk.ConfigureRenderStackEnvironment(plan) // before GTK initialization

// In cef.App.OnBeforeCommandLineProcessing:
cef2gtk.ConfigureCommandLine(commandLine, cef2gtk.CommandLineOptions{RenderStackPlan: plan})

view := cef2gtk.NewViewWithOptions(cef2gtk.ViewOptions{RenderStackPlan: plan})
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
cef2gtk.ConfigureBrowserSettings(&settings, cef2gtk.BrowserSettingsOptions{WindowlessFrameRate: 144})
client := cef.NewClient(myClient{render: view.RenderHandler(cef2gtk.Hooks{})})
cef.BrowserHostCreateBrowser(&info, client, "https://example.com/", &settings, nil, nil)

// In OnAfterCreated(browser cef.Browser), attach the BrowserHost:
_ = view.AttachInput(browser.GetHost(), cef2gtk.InputOptions{Scale: 0})
```

### Inertial scrolling

Touchpad inertia and wheel smoothing are opt-in per `InputOptions.Scroll`:

```go
_ = view.AttachInput(host, cef2gtk.InputOptions{
    Scroll: cef2gtk.ScrollOptions{TouchpadInertia: true, WheelSmoothing: true},
})
```

Zero values keep direct scroll behavior. Touchpad motion tracks input
directly; a valid release decays exponentially and stops on retouch,
modifiers, press, focus loss, hide, host replacement, or detach. Wheel
bursts interpolate accepted impulses across frames; Ctrl/Alt/Meta-modified
input always bypasses animation with direct physical delivery. Synthetic
output never passes through `OnScroll` or navigation recognition.

Cancellation splits immediate invalidation from GTK cleanup:
`InvalidateScroll` retires motion from any thread and returns the retired
epoch; `CancelScrollEpoch` cleans up that epoch on the GTK thread without
touching newer sessions. `CancelScroll` does both inline on the GTK thread.
Already-submitted CEF events cannot be retracted.

All engine timestamps share one monotonic clock so event intervals stay live
whether or not GTK frames render between input events.

For diagnosis, `PUREGO_CEF2GTK_SCROLL_TRACE=1` enables a bounded (2000-line)
stderr trace of input events (both monotonic and GDK timestamps, units,
deltas, coordinates, modifiers), tick lifecycle, engine decisions with
discarded quantities, and per-burst submission totals. It never logs page
content or URLs.

See `examples/simple-browser` for a complete accelerated-only GTK+CEF setup.

## Development

The module depends on the published `github.com/bnema/puregotk` module fork. 
