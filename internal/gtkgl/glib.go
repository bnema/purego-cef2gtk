package gtkgl

import (
	"fmt"

	"github.com/bnema/puregotk/v4/glib"
)

func glibErrorMessage(err *glib.Error) string {
	if err == nil {
		return "unknown GTK/GLib error"
	}
	return fmt.Sprintf("domain=%d code=%d message_ptr=0x%x", err.Domain, err.Code, err.Message)
}
