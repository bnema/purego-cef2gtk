# purego-cef2gtk

GTK4 widget bridge for [`purego-cef`](https://github.com/bnema/purego-cef) accelerated off-screen rendering.

## Status

Early bootstrap. The renderer is Wayland-only and GPU-first:

- CEF accelerated OSR with shared textures/DMABUFs
- GTK4 `GtkGLArea`
- EGL/OpenGL import and copy path
- no X11 target
- no `--disable-gpu` or software rendering fallback
- CEF `OnPaint` is treated as a diagnostic/error path, not as a renderer

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
_ = view.AttachInput(browser.GetHost(), cef2gtk.InputOptions{Scale: 1})
```

See `examples/simple-browser` for a complete accelerated-only GTK+CEF setup.

## Development

The module depends on the published `github.com/bnema/puregotk` module. During local puregotk development,
you can temporarily add your own `replace github.com/bnema/puregotk => ../puregotk` directive.

```sh
rtk make check
```
