# purego-cef2gtk

GTK4 widget bridge for [`purego-cef`](https://github.com/bnema/purego-cef) accelerated off-screen rendering.

## Status

Early bootstrap. The intended renderer is Wayland-only and GPU-first:

- CEF accelerated OSR with shared textures/DMABUFs
- GTK4 `GtkGLArea`
- EGL/OpenGL import and copy path
- no X11 target
- no `--disable-gpu` or software rendering fallback
- CEF `OnPaint` is treated as a diagnostic/error path, not as a renderer

## Development

```sh
rtk make check
```
