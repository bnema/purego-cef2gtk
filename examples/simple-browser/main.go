package main

import (
	"fmt"
	"net/url"
	"os"
	"runtime"

	"github.com/bnema/purego-cef/cef"
	cef2gtk "github.com/bnema/purego-cef2gtk"
	"github.com/bnema/puregotk/v4/glib"
	"github.com/bnema/puregotk/v4/gtk"
)

type app struct{}

func (app) OnBeforeCommandLineProcessing(_ string, commandLine cef.CommandLine) {
	cef2gtk.ConfigureCommandLine(commandLine, cef2gtk.CommandLineOptions{})
}
func (app) OnRegisterCustomSchemes(cef.SchemeRegistrar)         {}
func (app) GetResourceBundleHandler() cef.ResourceBundleHandler { return nil }
func (app) GetBrowserProcessHandler() cef.BrowserProcessHandler { return nil }
func (app) GetRenderProcessHandler() cef.RenderProcessHandler   { return nil }

type client struct {
	view *cef2gtk.View
	life *lifeSpan
}

func (c *client) GetRenderHandler() cef.RenderHandler {
	return c.view.RenderHandler(cef2gtk.Hooks{OnError: func(err error) { fmt.Fprintln(os.Stderr, "render:", err) }})
}
func (c *client) GetLifeSpanHandler() cef.LifeSpanHandler       { return c.life }
func (c *client) GetAudioHandler() cef.AudioHandler             { return nil }
func (c *client) GetCommandHandler() cef.CommandHandler         { return nil }
func (c *client) GetContextMenuHandler() cef.ContextMenuHandler { return nil }
func (c *client) GetDialogHandler() cef.DialogHandler           { return nil }
func (c *client) GetDisplayHandler() cef.DisplayHandler         { return nil }
func (c *client) GetDownloadHandler() cef.DownloadHandler       { return nil }
func (c *client) GetDragHandler() cef.DragHandler               { return nil }
func (c *client) GetFindHandler() cef.FindHandler               { return nil }
func (c *client) GetFocusHandler() cef.FocusHandler             { return nil }
func (c *client) GetFrameHandler() cef.FrameHandler             { return nil }
func (c *client) GetPermissionHandler() cef.PermissionHandler   { return nil }
func (c *client) GetJsdialogHandler() cef.JsdialogHandler       { return nil }
func (c *client) GetKeyboardHandler() cef.KeyboardHandler       { return nil }
func (c *client) GetLoadHandler() cef.LoadHandler               { return nil }
func (c *client) GetPrintHandler() cef.PrintHandler             { return nil }
func (c *client) GetRequestHandler() cef.RequestHandler         { return nil }
func (c *client) OnProcessMessageReceived(cef.Browser, cef.Frame, cef.ProcessID, cef.ProcessMessage) int32 {
	return 0
}

type lifeSpan struct{ view *cef2gtk.View }

func (l *lifeSpan) OnAfterCreated(browser cef.Browser) {
	if err := l.view.SetInputHost(browser.GetHost()); err != nil {
		fmt.Fprintln(os.Stderr, "set input host:", err)
	}
}
func (l *lifeSpan) OnBeforeClose(cef.Browser) {}
func (l *lifeSpan) DoClose(cef.Browser) bool  { return false }
func (l *lifeSpan) OnBeforePopup(cef.Browser, cef.Frame, int32, string, string, cef.WindowOpenDisposition, int32, *cef.PopupFeatures, *cef.WindowInfo, *cef.RawClientWriteSlot, *cef.BrowserSettings, *cef.DictionaryValue, *bool) bool {
	return false
}
func (l *lifeSpan) OnBeforePopupAborted(cef.Browser, int32) {}
func (l *lifeSpan) OnBeforeDevToolsPopup(cef.Browser, *cef.WindowInfo, *cef.RawClientWriteSlot, *cef.BrowserSettings, *cef.DictionaryValue, *bool) {
}

func main() {
	runtime.LockOSThread()
	gtk.Init()

	if err := cef.InitWithApp(cef.DefaultSettings(), app{}); err != nil {
		fmt.Fprintln(os.Stderr, "cef init:", err)
		os.Exit(1)
	}
	defer cef.Shutdown()

	view := cef2gtk.NewView()
	if view == nil {
		fmt.Fprintln(os.Stderr, "cef2gtk NewView failed")
		os.Exit(1)
	}
	defer func() {
		if err := view.Destroy(); err != nil {
			fmt.Fprintln(os.Stderr, "view destroy:", err)
		}
	}()

	window := gtk.NewWindow()
	window.SetDefaultSize(1024, 768)
	title := "purego-cef2gtk simple browser"
	window.SetTitle(&title)
	window.SetChild(view.Widget())
	window.Present()
	view.Widget().Realize()

	if err := view.PrepareOnGTKThread(); err != nil {
		fmt.Fprintln(os.Stderr, "prepare view:", err)
		os.Exit(1)
	}
	// AttachInput starts before the asynchronous browser exists; OnAfterCreated
	// later supplies the real host with SetInputHost(browser.GetHost()).
	// Scale=0 lets the view derive and track the current GTK surface scale.
	if err := view.AttachInput(nil, cef2gtk.InputOptions{Scale: 0}); err != nil {
		fmt.Fprintln(os.Stderr, "attach input:", err)
		os.Exit(1)
	}

	winInfo := cef.NewWindowInfo()
	cef2gtk.ConfigureWindowInfo(&winInfo, cef2gtk.WindowInfoOptions{})
	settings := cef.NewBrowserSettings()
	ls := &lifeSpan{view: view}
	startURL, err := urlArg()
	if err != nil {
		fmt.Fprintln(os.Stderr, "url:", err)
		os.Exit(1)
	}
	ok := cef.BrowserHostCreateBrowser(&winInfo, cef.NewClient(&client{view: view, life: ls}), startURL, &settings, nil, nil)
	if ok == 0 {
		fmt.Fprintln(os.Stderr, "cef BrowserHostCreateBrowser failed")
		os.Exit(1)
	}

	loop := glib.NewMainLoop(nil, false)

	closeCb := func(gtk.Window) bool { loop.Quit(); return false }
	window.ConnectCloseRequest(&closeCb)

	src := glib.SourceFunc(func(uintptr) bool {
		cef.DoMessageLoopWork()
		return true
	})
	glib.TimeoutAdd(10, &src, 0)

	loop.Run()
}

func urlArg() (string, error) {
	raw := "https://example.com/"
	if len(os.Args) > 1 {
		raw = os.Args[1]
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" {
		raw = "https://" + raw
		parsed, err = url.Parse(raw)
		if err != nil {
			return "", err
		}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	return parsed.String(), nil
}
