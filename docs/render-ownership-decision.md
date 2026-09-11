# GDK present ownership: decision record

Status: **blocked**. No renderer ownership change is implemented or proposed by
this record. It exists so the architecture decision in the roadmap can be made
against verified evidence instead of assumptions.

Question: may an accelerated CEF frame be retained (FD duplicated) and imported
asynchronously by GTK, or must its contents be copied into client-owned storage
before the CEF callback returns?

## 1. Verified environment

| Item | Value |
| --- | --- |
| Runtime | `cef-vaapi-bin 150.0.17-1`, `libcef.so` under the system CEF directory |
| Header version | `CEF_VERSION "150.0.17+g94c1726+chromium-150.0.7871.187"` |
| Header/Runtime match | Same package; the header and the shared object ship together |
| GTK / GSK | GTK 4.22.5, GSK Vulkan for the `vulkan` stack, GSK OpenGL for `egl` |

The contract is documented in the installed `cef_render_handler.h`:

> The underlying implementation uses a pool to deliver frames. As a result, the
> handle may differ every frame depending on how many frames are in-progress.
> The handle's resource cannot be cached and cannot be accessed outside of this
> callback. It should be reopened each time this callback is executed and the
> contents should be copied to a texture owned by the client application. The
> contents of `info` will be released back to the pool after this callback
> returns.

`CefRenderHandler` exposes `OnAcceleratedPaint` and no counterpart that returns,
retains, or releases the resource. There is no release protocol to call.

### Producer source availability

Two local CEF checkouts were inspected. They pin Chromium
`refs/tags/146.0.7680.0` (CEF master, commit `d29a3ecf`) and
`refs/tags/148.0.7778.0` (commit `05d7a247`). Neither matches the running
Chromium 150.0.7871.187, so **the exact producer/export/reuse sequence for the
runtime version is not locally available**.

The related-version code is consistent with the header and is recorded only as
supporting context: the Linux shared-texture path copies `plane.fd.get()` out of
the frame's `GpuMemoryBufferHandle` (`video_consumer_osr.cc`), and the handle is
owned by the frame data whose last reference is dropped when the callback and
its caller return. There is no CEF-side retention.

## 2. Four lifetimes that must not be conflated

| Lifetime | Observed state |
| --- | --- |
| FD close lifetime | The plane FD belongs to the SDK's buffer handle and is passed borrowed. Keeping a duplicate keeps the file open; it does not extend anything else. |
| Buffer pool reuse lifetime | Pool-owned. Contents may be overwritten after the callback returns. Not observable from the bridge. |
| Producer write completion | Not observable through the CEF API. No fence, event, or completion callback is exposed. |
| Consumer read completion | Not observable today: import is a draw submission with no wait. |

Descriptor identity is not buffer generation identity. `dup(2)` preserves
openness only; a duplicated descriptor does not make borrowed image contents
immutable.

## 3. Options

### Option A — retain the producer buffer with an explicit CEF release protocol

**Blocked: the protocol does not exist.** The header states the contents are
released to the pool when the callback returns, and there is no release entry
point to hold them beyond it. Required symbols: a retain/release pair on the
accelerated paint path. Present: none.

Scope if pursued: a CEF patch plus a renewed scope decision, which the current
scope excludes.

### Option B — copy GPU contents into client-owned storage, prove completion, then hand off

**Partially implemented; blocked on completion proof and cross-device
ordering.**

Already available in this repository:

- `internal/egl`: `eglCreateImageKHR`, `eglDestroyImageKHR`,
  `EGL_EXT_image_dma_buf_import` and its modifier variant, `EGL_KHR_image_base`.
- `internal/gl`: `glEGLImageTargetTexture2DOES` (extension-provided), texture and
  framebuffer binding, a textured-quad program, `DrawArrays`, `ReadPixels`, and
  GL timer queries.
- `gl.TexturedQuadCopier.CopyImportedToOwned` performs a real GPU copy:
  EGLImage import to a texture, framebuffer attach, textured-quad draw into a
  client-owned RGBA texture.
- `gtkgl.AcceleratedRenderer.ImportCopyAndQueue` uses that path for the `egl`
  render stack.

Verified blockers — none of these symbols exist in the repository:

- **Completion.** No `glFenceSync`, `glClientWaitSync`, `glFinish`, or `glFlush`
  binding, and no EGL sync object support (`eglCreateSyncKHR`, `eglWaitSyncKHR`).
  The driver advertises `EGL_KHR_fence_sync`, `EGL_KHR_wait_sync`, and
  `EGL_ANDROID_native_fence_sync` (see the probe output below), but the bridge
  binds none of them. Submitting a draw therefore proves nothing about when the
  source read finished.
- **Cross-device ordering.** The producer is ANGLE (Vulkan). The default
  `vulkan` stack never copies at all: it hands the borrowed DMA-BUF to
  `GdkDmabufTextureBuilder`, and GSK imports it with no completion callback —
  only texture finalization. There are no Vulkan bindings in this repository, and
  the plan excludes adding native Vulkan bindings, so an external
  semaphore/fence protocol with the producer's device cannot be established here.
- **Bounded client pool, backpressure and drop policy.** Not implemented. The
  current retirement list is a reference-retention queue, not a synchronized
  pool.
- **Readback.** `ReadPixels` exists but is CPU readback; using it in a production
  accelerated path is excluded by the plan's invariants.

Estimated scope for the GL-only variant: bind fences, add a bounded
client-owned texture pool with completion-based reuse, add an explicit
unsupported-path error, and keep the existing GDK path for the `vulkan` stack
until a producer-side protocol exists. The cross-device proof for the default
stack is the part that cannot be closed inside this repository.

### Option C — current duplicate-FD + asynchronous idle import

**Not acceptable as a correctness strategy and must not be extended.** It
preserves descriptor openness, not contents. After the callback returns, the
pool may hand the same buffer to the producer again while the GTK idle import is
still pending.

This is a latent content-tearing risk rather than a reproduced failure: no torn
or overwritten frame was observed during lab runs, and no visual artifact was
reported. The documented contract permits it, which is what makes it unsafe.

## 4. Probe evidence

`cmd/probe-import-copy` was run on the development machine under Wayland with a
GtkGLArea context, using its synthetic DMA-BUF path:

```json
{
  "status": "error",
  "error": "DrawTextureToCurrentFramebuffer: draw queued texture to GtkGLArea: GL error 0x506",
  "probe": {
    "egl_importer_created": true,
    "gl_backend_created": true,
    "copier_created": true,
    "dmabuf_allocated": true,
    "dmabuf_imported": true,
    "copy_pipeline_valid": true,
    "draw_pipeline_valid": false,
    "readback_match": true
  }
}
```

Honest reading, reported independently as required:

- A synthetic DMA-BUF was allocated and imported, the copy pipeline ran, and the
  readback matched the expected content. On this driver the import-and-copy path
  works in isolation.
- The final draw into the window's default framebuffer failed with
  `GL_INVALID_FRAMEBUFFER_OPERATION` (`0x506`); that step is not validated by
  this probe on this harness.
- This is **not** a Vulkan transfer proof, **not** the real
  ANGLE-to-consumer path, and **not** evidence of ownership safety.
  `copy_pipeline_valid=true` alone never satisfies the GPU-correctness
  criterion.

## 5. Proof test required before any implementation

Content, not descriptor openness:

1. Render generation `M1` into producer slot `A`, accept the frame, return from
   the callback, then immediately render generation `M2` into slot `A` while
   consumer consumption is deliberately delayed. The accepted frame must still
   read back `M1`.
2. Run on the real DMA-BUF hardware path with the modifier and sizes the runtime
   actually uses. A synthetic fallback is not acceptance.
3. Cover backpressure, resize and reallocation, destroy during copy, unsupported
   modifier, device loss, timeout, and resource accounting.
4. Treat a memfd-only test as a demonstration of `dup(2)` semantics. It cannot
   demonstrate GPU correctness.

## 6. Consequence

The render lab and the frame-pipeline instrumentation ship as
`instrumentation-only`. No ownership, pacing, stack default, or dependency
change is included, and the GPU-correctness and pacing criteria remain
unverified.
