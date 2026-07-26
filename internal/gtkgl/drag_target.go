package gtkgl

import (
	"github.com/bnema/purego-cef/cef"
	"github.com/bnema/purego-cef2gtk/internal/gtkdnd"
	"github.com/bnema/puregotk/v4/gdk"
)

func (b *DragBridge) dispatchTargetEnter(data cef.DragData, event *cef.MouseEvent, allowed cef.DragOperationsMask) {
	if data == nil {
		return
	}
	defer releaseCEFHandle(data)
	if h := b.currentHost(); h != nil {
		h.DragTargetDragEnter(data, event, allowed)
	}
}

func (b *DragBridge) targetEnterData(formats []string, own bool, text, uri string) cef.DragData {
	data := b.newMetadataData(gtkdnd.MetadataFromFormats(formats))
	if !own || text == "" {
		return data
	}
	releaseCEFHandle(data)
	data = b.newDragData()
	if data == nil {
		return nil
	}
	if uri != "" {
		data.SetLinkURL(uri)
	} else {
		data.SetFragmentText(text)
	}
	return data
}

func (b *DragBridge) targetEnter(drop *gdk.Drop, x, y float64) gdk.DragAction {
	if drop == nil || b.targetProtocol.Enter(drop.GoPointer()) == 0 {
		return gdk.ActionNoneValue
	}
	b.setActiveDrop(drop)
	formats := drop.GetFormats()
	var names []string
	if formats != nil {
		var n uint
		names = formats.GetMimeTypes(&n)
	}
	// For our own B-1 drag the in-memory text snapshot is authoritative and
	// available without beginning the B-2 asynchronous external reader.
	candidate := drop.GetDrag()
	ownDrop := candidate != nil
	if candidate != nil {
		candidate.Unref()
	}
	b.mu.Lock()
	own := ownDrop && b.sourceDrag != nil
	text, uri := b.sourceText, b.sourceLinkURL
	b.mu.Unlock()
	if own && text != "" {
		b.targetProtocol.MarkContentReal(drop.GoPointer())
	}
	data := b.targetEnterData(names, own, text, uri)
	offered := GDKToCEFDragActions(drop.GetActions())
	n := NegotiateDragActions(offered, cef.DragOperationsMaskDragOperationCopy|cef.DragOperationsMaskDragOperationMove|cef.DragOperationsMaskDragOperationLink)
	preferred := b.preferredAction(n)
	// GtkDropTargetAsync applies the returned action with its internal
	// gtk_drop_status() bookkeeping after this signal handler returns. Calling
	// GdkDrop.Status here would bypass that bookkeeping and can re-enter motion.
	e := b.dragMouseEvent(drop, x, y)
	b.dispatchTargetEnter(data, &e, n.Allowed)
	return CEFToGDKDragActions(preferred)
}
func (b *DragBridge) targetMotion(drop *gdk.Drop, x, y float64) gdk.DragAction {
	if drop == nil {
		return gdk.ActionNoneValue
	}
	e := b.dragMouseEvent(drop, x, y)
	decision := b.targetProtocol.Motion(drop.GoPointer(), e.X, e.Y, e.Modifiers)
	if decision == TargetMotionRejected {
		return gdk.ActionNoneValue
	}
	offered := GDKToCEFDragActions(drop.GetActions())
	n := NegotiateDragActions(offered, cef.DragOperationsMaskDragOperationCopy|cef.DragOperationsMaskDragOperationMove|cef.DragOperationsMaskDragOperationLink)
	preferred := b.preferredAction(n)
	if decision == TargetMotionDispatch {
		if h := b.currentHost(); h != nil {
			h.SendMouseMoveEvent(&e, 0)
			h.DragTargetDragOver(&e, n.Allowed)
		}
	}
	// Returning the preferred action lets GtkDropTargetAsync call its private
	// gtk_drop_status() exactly once. Status-induced duplicate motions are not
	// redispatched to CEF, which breaks the Wayland Copy/None feedback cycle.
	return CEFToGDKDragActions(preferred)
}
func (b *DragBridge) targetDrop(drop *gdk.Drop, x, y float64) bool {
	if drop == nil {
		return false
	}
	plan, ok := b.targetProtocol.BeginDrop(drop.GoPointer())
	if !ok {
		return false
	}
	b.clearActiveDrop(drop.GoPointer())
	drag := drop.GetDrag()
	traceDND("target-drop generation=%d own=%t actions=%d require_content_real=%t", plan.Generation, drag != nil, drop.GetActions(), plan.RequireContentReal)
	if drag == nil {
		formats := drop.GetFormats()
		var names []string
		if formats != nil {
			var count uint
			names = formats.GetMimeTypes(&count)
		}
		n := NegotiateDragActions(GDKToCEFDragActions(drop.GetActions()), cef.DragOperationsMaskDragOperationEvery)
		e := b.dragMouseEvent(drop, x, y)
		traceDND("external-drop advertised=%q offered=%d allowed=%d deterministic_preferred=%d selected_preferred=%d", names, n.Offered, n.Allowed, n.Preferred, b.preferredAction(n))
		b.retainDrop(drop)
		source := b.newDropSource(drop)
		if source == nil {
			b.targetProtocol.CompleteDrop(plan.Generation)
			drop.Finish(gdk.ActionNoneValue)
			b.releaseDrop(drop)
			return true
		}
		b.beginExternalDrop(plan, names, source, e, b.preferredAction(n), func(action gdk.DragAction) {
			drop.Finish(action)
			b.releaseDrop(drop)
		})
		return true
	}
	defer drag.Unref()
	action := drag.GetSelectedAction()
	if action == gdk.ActionNoneValue {
		n := NegotiateDragActions(GDKToCEFDragActions(drop.GetActions()), cef.DragOperationsMaskDragOperationEvery)
		action = CEFToGDKDragActions(n.Preferred)
	}
	e := b.dragMouseEvent(drop, x, y)
	if h := b.currentHost(); h != nil {
		h.DragTargetDrop(&e)
	}
	if gen, ok := b.protocol.CurrentGeneration(); ok {
		b.protocol.OwnDrop(gen, e.X, e.Y, GDKToCEFDragActions(action))
	}
	b.targetProtocol.CompleteDrop(plan.Generation)
	drop.Finish(action)
	return true
}

func (b *DragBridge) beginExternalDrop(plan TargetDropPlan, advertised []string, source releasableAsyncSource, event cef.MouseEvent, proposed cef.DragOperationsMask, finish func(gdk.DragAction)) {
	traceDND("external-read-start generation=%d proposed=%d advertised_count=%d", plan.Generation, proposed, len(advertised))
	if source == nil {
		finish(gdk.ActionNoneValue)
		return
	}
	b.externalReader.Read(advertised, source, func(result gtkdnd.ReadResult) {
		defer source.Release()
		finishAction := gdk.ActionNoneValue
		traceDND("external-read-done generation=%d mime=%q bytes=%d error=%v", result.Generation, result.MIME, len(result.Data), result.Err)
		if result.Err == nil {
			payload, err := gtkdnd.ParseInboundPayload(result.MIME, result.Data)
			traceDND("external-parse files=%d text=%t html=%t link=%t error=%v", len(payload.Files), payload.Text != "", payload.HTML != "", payload.LinkURL != "", err)
			if err == nil {
				b.mu.Lock()
				allow, makeData := b.fileDropAllowed, b.newInboundData
				b.mu.Unlock()
				accepted := gtkdnd.ApplyFileDropVeto(payload, proposed, allow)
				traceDND("external-policy proposed=%d accepted=%d", proposed, accepted)
				if accepted != cef.DragOperationsMaskDragOperationNone {
					dispatched := b.targetProtocol.DispatchDrop(plan.Generation, func(completed TargetDropPlan) {
						data := makeData(payload)
						if data == nil {
							return
						}
						defer releaseCEFHandle(data)
						if h := b.currentHost(); h != nil {
							if completed.RequireContentReal {
								h.DragTargetDragLeave()
								h.DragTargetDragEnter(data, &event, accepted)
								h.DragTargetDragOver(&event, accepted)
							}
							h.DragTargetDrop(&event)
							finishAction = CEFToGDKDragActions(accepted)
						}
					})
					traceDND("external-dispatch generation=%d dispatched=%t finish_action=%d", plan.Generation, dispatched, finishAction)
				}
			}
		}
		// Errors, refusals, and stale callbacks can only close their own
		// generation and retained drop.
		b.targetProtocol.CompleteDrop(plan.Generation)
		traceDND("external-finish generation=%d action=%d", plan.Generation, finishAction)
		finish(finishAction)
	})
}
