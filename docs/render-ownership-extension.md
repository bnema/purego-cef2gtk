# Render ownership extension: approval artifact

Status: **draft for approval**. No production code is included. This is the
artifact Phase P4 asks for: exact APIs, resource types, synchronization
ordering, file and task scope, and the tests that would close the gate.

Companion record: `docs/render-ownership-decision.md` (evidence, blocked
options, upstream check).

## 1. Decision requested

- **D1 — answered: the `vulkan` stack stays the production default.** Owner:
  human, 2026-09-11. EGL is a fallback, not a default. This selects **variant B**
  (§6) and demotes variant A (§5) to the fallback path.
- **D2 — still open: accept the producer-visibility assumption (§4), or authorise
  a bounded investigation to try to close it?** Accepting it means the
  GPU-correctness criterion is amended, not satisfied: acceptance becomes
  *conditional consumer-ownership acceptance*.
- **D3 — new, opened by D1: what is the release signal for the client-owned
  destination?** §12 shows that GDK and GSK expose none. Variant B cannot state a
  completion-based pool reuse rule without deciding this.

Also confirmed by this artifact: the work is split into two sequential lots
(§8), **ownership first**. Pacing-first is not approvable under the current
wording of the pacing criterion.

## 2. Evidence baseline and the four distinct obligations

Evidence: CEF 150.0.17 headers, `cmd/probe-import-copy` output, driver EGL
extension list, and the upstream comparison in the decision record §7. Summary:
the accelerated-paint resource is borrowed, is documented as released to its
pool when the callback returns, and no CEF entry point retains it or orders a
consumer read against it.

Four obligations that must not be conflated:

| # | Obligation | Status today |
| --- | --- | --- |
| 1 | Source descriptor stays open while we use it | handled by `dup` |
| 2 | Source contents do not change under the consumer | **not handled**, and not expressible without a copy |
| 3 | The consumer's read of the source has completed before we return the borrowed resource | **not handled**: import is a queue-and-idle submission with no wait |
| 4 | Owned destination stays immutable while downstream still reads it | **not handled**: a retirement list retention is not a release protocol |

Obligation 3 is the one that closes the pool-reuse hazard. Obligation 4 is the
one that a naive "owned texture" change misses.

## 3. Requested amendments

- **GPU-correctness criterion.** Replace "unknown synchronization is a blocker"
  with a two-part statement: *consumer ownership is demonstrated* (obligations
  2 and 3 satisfied, verified by the generation-reuse test in §11), and
  *producer-visibility is an accepted assumption* (§4) recorded with its
  residual risk. Do not report the original unqualified criterion as satisfied.
- **Pacing criterion.** Its explicit dependency on GPU correctness is kept.
  Lot B (§8) may not ship before Lot A. Scheduler modelling, instrumentation and
  experiments may proceed earlier, but they do not satisfy the criterion.
- **Delivery gate.** The two lots are required together for the phase to count
  as complete; splitting is for attribution and rollback, not for partial credit.

## 4. Producer-visibility assumption (proposed wording)

> For each accepted accelerated-paint callback on the supported runtime and
> driver configuration, the supplied DMA-BUF's intended frame writes are either
> complete and visible to the importing consumer before sampling, or ordered
> before that sampling by a mechanism honoured by both exporter and importer.
> The bridge does not establish or independently verify this producer-to-consumer
> dependency through the CEF API. A successful consumer fence wait establishes
> completion of the bridge's own copy before the callback returns; it does not
> independently establish producer-write visibility. Residual risk: absent or
> incorrect producer or import synchronization can yield stale or partially
> written pixels inside an otherwise independently owned frame.

This is the honest reason the original criterion cannot be closed in-tree: the
absence of a fence parameter in the CEF API does not prove the producer is
unsynchronised, and it does not give us a way to check. A bounded investigation
(§9) could replace this assumption with evidence; until then it is an assumption.

Supported configuration matrix (to be recorded by the human, per machine):
runtime CEF build, driver, GPU, render stack, and whether the synthetic
producer test in §11 passes there.

## 5. Design variant A — fallback path: qualified copy on the `egl` stack

Not the default: D1 requires `vulkan` presentation. It remains the fallback if
variant B proves unapprovable, and it is the smaller of the two changes because
the import and copy pipeline already exists and is exercised. Kept in this
artifact so the fallback is specified rather than improvised.

Native APIs to add:

- `glFenceSync(GL_SYNC_GPU_COMMANDS_COMPLETE, 0)` and
  `glClientWaitSync(sync, GL_SYNC_FLUSH_COMMANDS_BIT, timeout_ns)` on the
  GtkGLArea context, resolved through the existing extension-proc path in
  `internal/gl/loader.go`.
- `glDeleteSync`. `glFinish` is not used in production; it stays a diagnostic.

Ordering, per accepted frame, on the GTK thread with the copying context
current:

1. Import the borrowed frame's EGLImage and bind it as a texture.
2. Draw the textured quad into an owned destination texture from the bounded
   pool (existing `gl.TexturedQuadCopier.CopyImportedToOwned`).
3. Insert the fence.
4. `glClientWaitSync` with a bounded timeout, accepting only
   `GL_ALREADY_SIGNALED` or `GL_CONDITION_SATISFIED`.
5. On success: release the imported source objects, mark the destination
   complete, and hand it to GSK.
6. On timeout: see §7. Never return to CEF leaving an outstanding read of a
   borrowed resource.

File scope: `internal/gl/copy.go` (+ fence bindings in `internal/gl/loader.go`
and their tests), `internal/gtkgl/accelerated_renderer.go` (+ tests), the pool
type in `internal/gl` or `internal/gtkgl`, and the unsupported-path error
surface. No CEF binding change, no new dependency.

Explicitly out of scope for variant A: making `vulkan` safe. Under variant A the
`vulkan` stack keeps its known contract violation and must say so
(§7, unsupported-path behaviour).

## 6. Design variant B — selected: keep GSK Vulkan presentation with client-owned DMA-BUFs

Selected by D1. It is **not** a dominating option: it keeps the default stack and
adds cross-API interop on top of variant A's work.

### A copy and export engine is required, and it does not exist yet

The `vulkan` stack presents through GtkPicture and GdkDmabufTexture; there is no
GtkGLArea and therefore **no current GL context** to copy or export with. The
bridge must own an offscreen EGL/GL context used only as a copy and export
engine:

- context creation from a render node, with `EGL_KHR_surfaceless_context` (the
driver advertises it) and an explicit config/format negotiation;
- extension validation at init (`EGL_EXT_image_dma_buf_import` and its modifier
variant for the source, `EGL_MESA_image_dma_buf_export` for the destination),
with an explicit unsupported error rather than a fallback;
- GTK-thread affinity and teardown, alongside the existing widget lifecycle;
- no presentation duties: it never draws to a window.

This subsystem is new work and must appear in the file scope, not be discovered
during implementation.

Native APIs to add on top of variant A's fence work:

- `eglCreateImageKHR` on an exportable allocation plus
  `eglExportDMABUFImageQueryMESA` / `eglExportDMABUFImageMESA` (driver advertises
  `EGL_MESA_image_dma_buf_export`), or an equivalent export path;
- the same fence completion as variant A before the exported buffer is handed on;
- a GDK side owned-DMA-BUF texture build path plus the pool release rule from
  §12, which is currently undecided.

Additional proofs this variant owes, in order: exportable allocation path,
format and modifier compatibility with the GSK import, visibility of our own
writes to GSK, and bounded retirement of the owned pool after downstream
consumption. The existing GDK destroy notification closes a descriptor when GDK
finalizes the texture; it is a reference drop, not a GPU completion.

## 7. Admission, drops, timeouts, unsupported paths

- **Pre-submission drop is safe**: before any GPU read of a borrowed source is
  submitted, the frame may be dropped and the callback returned. Newest eligible
  frame wins, bounded by a small queue and an explicit policy.
- **Post-submission timeout is not a drop**. Once a read of a borrowed source is
  submitted, a fence timeout leaves that read outstanding; returning to CEF at
  that point is a use-after-release. The extension must define a drain or
  termination policy (block until completion, or fail the frame *and* the
  pipeline state) and state its bound. This is currently unspecified and is a
  blocker for approval until decided.
- **Unsupported path**: report an explicit error and retain the last valid
  content where appropriate. No silent CPU readback, no automatic render-stack
  change. A mode that rejects unsupported ownership must be distinguishable from
  a legacy mode that knowingly keeps the borrowed path.
- **Device loss / context loss**: same rule as timeout: the pipeline transitions
  to an explicit failed state rather than quietly returning a borrowed resource.

## 8. Lots

- **Lot A — ownership (first)**: fence completion, bounded owned pool with
  completion-based reuse, downstream retirement, failure and timeout policy,
  unsupported-path error, native generation-reuse test, and the correction of
  the misleading ownership claim in the current GDK import doc comment.
  Scheduling stays as it is.
- **Lot B — pacing (second, after Lot A qualifies)**: replace idle scheduling
  with a defined GTK frame-clock state machine using the real
  `ConnectUpdate`, `RequestPhase`, `BeginUpdating`/`EndUpdating` bindings: one
  safe-frame selection per chosen update phase, frames after selection go to the
  next opportunity, hidden widgets stop consuming, remap wakes, resize
  generations cannot present stale dimensions, empty scenes do not drive
  continuous ticks, input latency and popup routing preserved. External
  BeginFrame remains a separate opt-in variable; it is not changed in the same
  comparison.

Pacing-first is rejected: the pacing criterion's dependency on GPU correctness
is explicit, and moving borrowed-frame import to a later opportunity can
increase borrowed-buffer exposure rather than reduce it.

## 9. Optional bounded investigation (alternative to accepting §4)

A timeboxed investigation of the actual producer and importer synchronization
contract: whether the exporter publishes a reservation fence and whether the
importer honours it, on this machine's runtime. Deliverable: either evidence
that closes the producer-visibility obligation, or a documented "cannot
determine" with what was inspected. This is what would let the original
criterion be met rather than amended. It requires CEF/Chromium sources for the
matching revision (local clones are older) and is therefore a decision, not a
default.

## 10. Resource types and pool

- Owned destination textures, allocated once, capacity justified and small.
- A destination returns to the pool only after downstream consumption is known,
  never because a fixed number of frames elapsed.
- The retirement list is not a substitute for that protocol and shrinks or
  disappears rather than being extended.
- Bounded producer sequence and presentation opportunity stay distinct counters:
  "produced" is not "presented".

## 11. Tests that must pass, and what they cannot prove

Native, on real hardware, content-based:

1. **Generation reuse (the gate)**: render `M1` into producer slot `A`, accept
   the frame, return from the callback, immediately render `M2` into the same
   slot while consumer consumption is delayed; the accepted frame must still
   read back `M1`. This test must fail with the current borrowed strategy and
   pass with the approved one.
2. **Consumer completion**: with the copy submitted and the fence waited on,
   freeing or reusing the source must not alter the owned frame.
3. **Owned destination lifetime**: after downstream consumption is signalled,
   pool reuse must not corrupt the presented frame; the test must fail if
   retirement is driven by a frame count instead of completion.
4. **Failure paths**: pre-submission drop, post-submission timeout, unsupported
   modifier or format, resize and reallocation, destroy during copy, device
   loss, and resource accounting with no leaked texture, fence, or descriptor.
5. **End-to-end presentation**: first frame, resize, output scale change,
   hide/show, repeated close, popup controls, cursor and focus, on the qualified
   configuration, plus a timing comparison against the current path using
   quantiles on the same scene, size and device.

What these tests cannot prove: that CEF's exporter publishes the dependencies the
importer needs. Test 1 with a synthetic producer we synchronise ourselves
deliberately removes that uncertainty rather than measuring it.

Performance attribution note: the measured current path costs about 24
microseconds per frame on the GTK thread (about 2.9 ms per second at 120 Hz) and
about 51 microseconds from receipt to swap. The second figure is latency, not
CPU consumption. Neither figure establishes that scheduling is the dominant
cause of any user-visible symptom; pacing work is an experiment with a measured
outcome, not a promised remedy.

## 12. Downstream consumption: the release signal does not exist (D3)

Checked in the bindings available to this repository:

- `GdkTexture` exposes **no signal at all** (`Connect*` count is zero) and no
  fence, query, or completion API. Its only observable lifetime event is GObject
  finalization.
- `GtkGraphicsOffload` exposes only properties: child, enabled, black background,
  accessible id. No buffer release.
- The DMA-BUF builder's destroy notify is documented as running when GDK
  releases the texture, which is a reference drop, not a guarantee that the GPU
  finished reading the buffer.
- `GdkDmabufTextureBuilder.GetUpdateTexture` exists and describes in-place
  updates of an existing texture, which is the opposite of ownership.

So a pool rule of the form "reuse the destination once the consumer is done" has
no consumer-done signal to read. Three honest resolutions, to be decided:

1. **Second assumption.** Finalization is treated as "downstream reads
   completed", stated as an assumption with its residual risk, symmetric to §4.
   Cheapest, and the weakest: a driver that defers a release internally does not
   extend that guarantee to a buffer the bridge rewrites itself.
2. **Never rewrite a referenced buffer.** A bounded pool of N destinations, a
   buffer returns to the pool only after finalization, and if no buffer is free
   the frame is dropped pre-submission (which §7 already defines as safe). This
   respects "never recycle because a fixed number of frames elapsed" and adds an
   explicit drop policy, but it reduces the race window rather than removing it:
   the same residual read-after-release concern as (1) remains, because
   finalization is still the only signal.
3. **Find a real signal.** No such API was found in the bindings. This option
   means investigating GTK/GSK internals or upstream for a usable release or
   fence, and it may legitimately end in "cannot be closed here".

D3 must be answered before variant B can be called approvable: options 1 and 2
are assumptions that must be written into the acceptance wording, and option 3 is
a bounded investigation like §9.

## 13. Risks, exclusions, sign-off

Risks: a perfectly copied wrong generation (missing producer dependency);
callback or thread blocking on a GPU wait, with no safe return on timeout;
incomplete downstream ownership recreating corruption after the CEF lifetime
problem is fixed.

Excluded: CEF patches or forks, native Vulkan bindings, CPU readback in a
production path, silent render-stack changes, packaging or release changes.

Sign-off required on: D2, D3, the §4 wording if accepted, the §7 timeout policy,
and the §11 acceptance criteria.
