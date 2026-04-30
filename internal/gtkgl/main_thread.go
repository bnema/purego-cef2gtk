package gtkgl

import "github.com/bnema/puregotk/v4/glib"

// RunOnGTKThreadSync runs fn on the default GTK main context and waits for it to
// finish. If the current thread already owns that context, fn runs inline.
func RunOnGTKThreadSync(fn func()) {
	if fn == nil {
		return
	}
	ctx := glib.MainContextDefault()
	if ctx != nil && ctx.IsOwner() {
		fn()
		return
	}
	done := make(chan struct{})
	cb := glib.SourceOnceFunc(func(uintptr) {
		defer close(done)
		fn()
	})
	glib.IdleAddOnce(&cb, 0)
	<-done
}
