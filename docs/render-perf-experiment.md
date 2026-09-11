# Render-path performance experiment

One build carries five changes to the accelerated frame path, so a single binary
can be judged for smoothness and throughput. Every change is switchable at
runtime, and the defaults are the changed behaviour. This is an unverified
comparison build: nothing here is a correctness fix, and none of it establishes
buffer ownership.

The knobs apply to the GDK DMA-BUF presenter, which is the `vulkan` render stack.
The `egl` stack is unaffected.

## Knobs

| Knob | Default | Opt-out | Effect |
|---|---|---|---|
| `PUREGO_CEF2GTK_GDK_GRAPHICS_OFFLOAD` | `1` | `0`, `false`, `no`, `off` | Wraps the presenter picture in `GtkGraphicsOffload` so the compositor consumes the DMA-BUF instead of compositing the picture through GSK. |
| `PUREGO_CEF2GTK_GDK_IMPORT_PRIORITY` | `default` | `idle` | Priority of the GTK-thread frame import. `default` is GLib priority 0; `idle` restores `G_PRIORITY_DEFAULT_IDLE` (200), which runs after ordinary main-loop work. |
| `PUREGO_CEF2GTK_GDK_RETIRED_TEXTURES` | `2` | `1`..`16` | How many superseded textures stay referenced after the presenter moved on. Lower bounds descriptor and texture retention; it is not a synchronisation primitive. |
| `DUMBER_CEF_WINDOWLESS_FRAME_RATE` | unset | set to an integer | Dumber only. Pins CEF's OSR frame rate and suspends adaptive monitor-refresh polling. |
| `DUMBER_CEF_EXTERNAL_BEGIN_FRAME` | `1` | `0`, `false`, `no`, `off` | Dumber only. CEF produces frames on BeginFrame ticks driven by the GTK frame clock instead of its own timer. |

Values outside the documented range fall back to the default, and an unparsable
value never fails a launch.

## What each change can and cannot do

- **Compositor offload** removes a GSK composition step per presented frame. It
  also changes presentation semantics: offloaded content is drawn by the
  compositor as an opaque subsurface, so it does not participate in GSK effects
  applied above it. Verify rounded corners, fractional scale and popups before
  treating it as a default.
- **Import priority** moves the import earlier within the main loop, which can
  turn a frame that would land in the next frame-clock cycle into one that lands
  in the current one. It shortens latency; it does not create throughput.
- **Texture retention** bounds memory and open descriptors. It does not make
  presentation faster, and a value of `1` leaves no margin if a consumer still
  holds the previous texture.
- **Pinned frame rate** and **external BeginFrame** change the cadence at which
  frames are produced. They are the only knobs here that can raise the frame
  rate a display actually receives.

## Attribution

The comparison build accepts all changes at once, so a single run cannot say
which one caused a difference. The knobs exist for that: each can be reverted
individually without rebuilding, and the lab records the values it applied under
`render_knobs` in every run record.

The plan this work descends from requires one variable at a time. That rule is
deliberately suspended for this experiment at the operator's request, and the
result must not be presented as a validated default configuration.
