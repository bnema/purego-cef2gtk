package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bnema/purego-cef/cef"
	cef2gtk "github.com/bnema/purego-cef2gtk"
	"github.com/bnema/puregotk/v4/glib"
	"github.com/bnema/puregotk/v4/gtk"
)

const (
	cefWaylandSmokeEnv     = "PUREGO_CEF2GTK_WESTON_CEF_SMOKE"
	cefWaylandGlobalLimit  = 45 * time.Second
	cefWaylandPhaseLimit   = 10 * time.Second
	westonStartupLimit     = 10 * time.Second
	compositorCleanupLimit = 3 * time.Second
)

// TestCEFWestonHeadlessSmoke is intentionally opt-in because it requires a
// local Weston and CEF runtime. Once opted in, every prerequisite and lifecycle
// failure is fatal rather than a skip.
func TestCEFWestonHeadlessSmoke(t *testing.T) {
	if os.Getenv(cefWaylandSmokeEnv) == "" {
		t.Skipf("%s is not set; skipping opt-in Weston/CEF smoke", cefWaylandSmokeEnv)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	private := newSmokePrivateDirs(t)
	t.Setenv("XDG_RUNTIME_DIR", private.runtime)
	t.Setenv("XDG_STATE_HOME", private.state)
	t.Setenv("XDG_CACHE_HOME", private.cache)
	t.Setenv("XDG_CONFIG_HOME", private.config)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("GDK_BACKEND", "wayland")

	weston := startWeston(t, private)
	defer weston.stop(t, private.state)

	global, cancel := context.WithTimeout(context.Background(), cefWaylandGlobalLimit)
	defer cancel()
	if err := runCEFWestonLifecycle(global, private.state); err != nil {
		t.Fatalf("CEF Weston headless smoke failed: %v", err)
	}
}

type smokePrivateDirs struct {
	runtime string
	state   string
	cache   string
	config  string
}

func newSmokePrivateDirs(t *testing.T) smokePrivateDirs {
	t.Helper()
	root := t.TempDir()
	dirs := smokePrivateDirs{
		runtime: filepath.Join(root, "runtime"),
		state:   filepath.Join(root, "state"),
		cache:   filepath.Join(root, "cache"),
		config:  filepath.Join(root, "config"),
	}
	for _, dir := range []string{dirs.runtime, dirs.state, dirs.cache, dirs.config} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("create private smoke directory: %v", err)
		}
	}
	return dirs
}

type westonProcess struct {
	cmd    *exec.Cmd
	output bytes.Buffer
}

func startWeston(t *testing.T, dirs smokePrivateDirs) *westonProcess {
	t.Helper()
	westonPath, err := exec.LookPath("weston")
	if err != nil {
		t.Fatalf("Weston prerequisite unavailable with %s set: %v", cefWaylandSmokeEnv, err)
	}
	weston := &westonProcess{cmd: exec.Command(westonPath,
		"--backend=headless-backend.so", "--socket=wayland-0", "--idle-time=0")}
	weston.cmd.Dir = dirs.state
	weston.cmd.Stdout = &weston.output
	weston.cmd.Stderr = &weston.output
	weston.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := weston.cmd.Start(); err != nil {
		t.Fatalf("start Weston headless compositor: %v", err)
	}

	deadline := time.Now().Add(westonStartupLimit)
	socket := filepath.Join(dirs.runtime, "wayland-0")
	for time.Now().Before(deadline) {
		if err := weston.cmd.Process.Signal(syscall.Signal(0)); err != nil {
			weston.stop(t, dirs.state)
			t.Fatalf("Weston exited before its Wayland socket was ready")
		}
		if info, err := os.Stat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			return weston
		}
		time.Sleep(20 * time.Millisecond)
	}
	weston.stop(t, dirs.state)
	t.Fatalf("Weston Wayland socket was not ready before bounded startup deadline")
	return nil
}

func (w *westonProcess) stop(t *testing.T, stateDir string) {
	t.Helper()
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		return
	}
	defer writeSanitizedArtifact(t, filepath.Join(stateDir, "weston.log"), w.output.String())

	_ = syscall.Kill(-w.cmd.Process.Pid, syscall.SIGTERM)
	waited := make(chan error, 1)
	go func() { waited <- w.cmd.Wait() }()
	select {
	case <-waited:
		return
	case <-time.After(compositorCleanupLimit):
		_ = syscall.Kill(-w.cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-waited:
			return
		case <-time.After(compositorCleanupLimit):
			t.Errorf("Weston process group did not stop after bounded TERM and KILL")
		}
	}
}

var (
	absolutePathPattern = regexp.MustCompile(`(?:^|\s)/[^\s]+`)
	urlPattern          = regexp.MustCompile(`\b(?:https?|file)://[^\s]+`)
	deviceIDPattern     = regexp.MustCompile(`(?i)\b(?:gpu|device|vendor|renderer)(?:[_ -]?id)?\s*[=:]\s*[^\s]+`)
)

func writeSanitizedArtifact(t *testing.T, path, content string) {
	t.Helper()
	content = urlPattern.ReplaceAllString(content, "<url>")
	content = absolutePathPattern.ReplaceAllStringFunc(content, func(value string) string {
		if strings.HasPrefix(value, " ") {
			return " <path>"
		}
		return "<path>"
	})
	content = deviceIDPattern.ReplaceAllString(content, "<device>")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Errorf("write sanitized smoke artifact: %v", err)
	}
}

type cefSmokeApp struct{ plan cef2gtk.RenderStackPlan }

func (a cefSmokeApp) OnBeforeCommandLineProcessing(_ string, commandLine cef.CommandLine) {
	cef2gtk.ConfigureCommandLine(commandLine, cef2gtk.CommandLineOptions{RenderStackPlan: a.plan})
}
func (cefSmokeApp) OnRegisterCustomSchemes(cef.SchemeRegistrar)         {}
func (cefSmokeApp) GetResourceBundleHandler() cef.ResourceBundleHandler { return nil }
func (cefSmokeApp) GetBrowserProcessHandler() cef.BrowserProcessHandler { return nil }
func (cefSmokeApp) GetRenderProcessHandler() cef.RenderProcessHandler   { return nil }

type cefSmokeState struct {
	waiter       *lifecycleWaiter
	mu           sync.Mutex
	browser      cef.Browser
	resizeArmed  bool
	resizeWidth  int32
	resizeHeight int32
}

func (s *cefSmokeState) setBrowser(browser cef.Browser) {
	s.mu.Lock()
	s.browser = browser
	s.mu.Unlock()
}

func (s *cefSmokeState) currentBrowser() cef.Browser {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.browser
}

func (s *cefSmokeState) armResize(width, height int32) {
	s.mu.Lock()
	s.resizeArmed = true
	s.resizeWidth = width
	s.resizeHeight = height
	s.mu.Unlock()
}

func (s *cefSmokeState) observeSize(width, height int32) {
	s.mu.Lock()
	armed := s.resizeArmed
	changed := width != s.resizeWidth || height != s.resizeHeight
	s.mu.Unlock()
	if armed && changed {
		s.waiter.observe(milestoneResize)
	}
}

type cefSmokeClient struct {
	view  *cef2gtk.View
	state *cefSmokeState
}

func (c *cefSmokeClient) GetRenderHandler() cef.RenderHandler {
	return c.view.RenderHandler(cef2gtk.Hooks{
		OnFirstAcceleratedPaint:  func() { c.state.waiter.observe(milestoneAcceleratedPaint) },
		OnFirstDMABUFTextureSwap: func() { c.state.waiter.observe(milestoneDMABUFTextureSwap) },
		OnFirstPresentation:      func() { c.state.waiter.observe(milestonePresentation) },
		OnUnsupportedPaint:       func() { c.state.waiter.fail(errors.New("CEF supplied unsupported software paint")) },
		OnDMABUFUnsupported:      func() { c.state.waiter.fail(errors.New("DMABUF texture swap is unavailable")) },
		OnError:                  func(error) { c.state.waiter.fail(errors.New("CEF render path reported an error")) },
	})
}
func (c *cefSmokeClient) GetLifeSpanHandler() cef.LifeSpanHandler       { return (*cefSmokeLife)(c) }
func (c *cefSmokeClient) GetAudioHandler() cef.AudioHandler             { return nil }
func (c *cefSmokeClient) GetCommandHandler() cef.CommandHandler         { return nil }
func (c *cefSmokeClient) GetContextMenuHandler() cef.ContextMenuHandler { return nil }
func (c *cefSmokeClient) GetDialogHandler() cef.DialogHandler           { return nil }
func (c *cefSmokeClient) GetDisplayHandler() cef.DisplayHandler         { return nil }
func (c *cefSmokeClient) GetDownloadHandler() cef.DownloadHandler       { return nil }
func (c *cefSmokeClient) GetDragHandler() cef.DragHandler               { return nil }
func (c *cefSmokeClient) GetFindHandler() cef.FindHandler               { return nil }
func (c *cefSmokeClient) GetFocusHandler() cef.FocusHandler             { return nil }
func (c *cefSmokeClient) GetFrameHandler() cef.FrameHandler             { return nil }
func (c *cefSmokeClient) GetPermissionHandler() cef.PermissionHandler   { return nil }
func (c *cefSmokeClient) GetJsdialogHandler() cef.JsdialogHandler       { return nil }
func (c *cefSmokeClient) GetKeyboardHandler() cef.KeyboardHandler       { return nil }
func (c *cefSmokeClient) GetLoadHandler() cef.LoadHandler               { return nil }
func (c *cefSmokeClient) GetPrintHandler() cef.PrintHandler             { return nil }
func (c *cefSmokeClient) GetRequestHandler() cef.RequestHandler         { return nil }
func (c *cefSmokeClient) OnProcessMessageReceived(cef.Browser, cef.Frame, cef.ProcessID, cef.ProcessMessage) int32 {
	return 0
}

type cefSmokeLife cefSmokeClient

func (l *cefSmokeLife) OnAfterCreated(browser cef.Browser) {
	client := (*cefSmokeClient)(l)
	if err := client.view.SetInputHost(browser.GetHost()); err != nil {
		client.state.waiter.fail(errors.New("attach CEF input host"))
		return
	}
	client.state.setBrowser(browser)
}
func (*cefSmokeLife) DoClose(cef.Browser) bool { return false }
func (l *cefSmokeLife) OnBeforeClose(cef.Browser) {
	(*cefSmokeClient)(l).state.waiter.observe(milestoneClose)
}
func (*cefSmokeLife) OnBeforePopup(cef.Browser, cef.Frame, int32, string, string, cef.WindowOpenDisposition, int32, *cef.PopupFeatures, *cef.WindowInfo, *cef.RawClientWriteSlot, *cef.BrowserSettings, *cef.DictionaryValue, *bool) bool {
	return false
}
func (*cefSmokeLife) OnBeforePopupAborted(cef.Browser, int32) {}
func (*cefSmokeLife) OnBeforeDevToolsPopup(cef.Browser, *cef.WindowInfo, *cef.RawClientWriteSlot, *cef.BrowserSettings, *cef.DictionaryValue, *bool) {
}

func runCEFWestonLifecycle(global context.Context, stateDir string) (err error) {
	plan, err := cef2gtk.ResolveRenderStack(cef2gtk.RenderStackVulkan)
	if err != nil {
		return fmt.Errorf("resolve Vulkan render stack: %w", err)
	}
	cef2gtk.ConfigureRenderStackEnvironment(plan)
	gtk.Init()

	settings := cef.DefaultSettings()
	settings.RootCachePath = stateDir
	settings.CachePath = filepath.Join(stateDir, "cef-cache")
	if err := cef.InitWithApp(settings, cefSmokeApp{plan: plan}); err != nil {
		return errors.New("initialize CEF runtime")
	}
	cefLive := true
	defer func() {
		if cefLive {
			cef.Shutdown()
		}
	}()

	view := cef2gtk.NewViewWithOptions(cef2gtk.ViewOptions{Backend: plan.Backend})
	if view == nil {
		return errors.New("create DMABUF CEF view")
	}
	viewLive := true
	defer func() {
		if viewLive {
			_ = view.Destroy()
		}
	}()
	window := gtk.NewWindow()
	windowLive := true
	defer func() {
		if windowLive {
			window.Destroy()
		}
	}()

	window.SetDefaultSize(640, 480)
	window.SetChild(view.Widget())
	state := &cefSmokeState{waiter: newLifecycleWaiter()}
	removeSizeObserver := view.AddSizeObserver(state.observeSize)
	defer removeSizeObserver()
	window.Present()
	view.Widget().Realize()
	if err := view.PrepareOnGTKThread(); err != nil {
		return errors.New("prepare GTK DMABUF view")
	}
	if err := view.AttachInput(nil, cef2gtk.InputOptions{Scale: 0}); err != nil {
		return errors.New("attach GTK input")
	}

	info := cef.NewWindowInfo()
	cef2gtk.ConfigureWindowInfo(&info, cef2gtk.WindowInfoOptions{})
	browserSettings := cef.NewBrowserSettings()
	cef2gtk.ConfigureBrowserSettings(&browserSettings, cef2gtk.BrowserSettingsOptions{WindowlessFrameRate: 60})
	client := &cefSmokeClient{view: view, state: state}
	if cef.BrowserHostCreateBrowser(&info, cef.NewClient(client), "data:text/html,cef-wayland-smoke", &browserSettings, nil, nil) == 0 {
		return errors.New("create CEF browser")
	}

	if err := pumpUntil(global, cefWaylandPhaseLimit, state.waiter, milestonePresentation); err != nil {
		return err
	}
	browser := state.currentBrowser()
	if browser == nil || browser.GetHost() == nil {
		return errors.New("browser creation callback was not observed")
	}

	width, height := view.Size()
	state.armResize(width, height)
	window.SetDefaultSize(800, 600)
	browser.GetHost().WasResized()
	if err := pumpUntil(global, cefWaylandPhaseLimit, state.waiter, milestoneResize); err != nil {
		return err
	}

	browser.GetHost().CloseBrowser(1)
	if err := pumpUntil(global, cefWaylandPhaseLimit, state.waiter, milestoneClose); err != nil {
		return err
	}

	diagnostics := view.Diagnostics()
	writeSanitizedArtifactNoTest(filepath.Join(stateDir, "cef-smoke-summary.txt"), fmt.Sprintf(
		"accelerated_paints=%d\npaintable_swaps=%d\nimport_failures=%d\nrender_failures=%d\n",
		diagnostics.AcceleratedPaints, diagnostics.PaintableSwaps, diagnostics.ImportFailures, diagnostics.RenderFailures))
	if diagnostics.AcceleratedPaints == 0 || diagnostics.PaintableSwaps == 0 {
		return errors.New("diagnostics did not confirm accelerated paint and DMABUF texture swap")
	}

	if err := view.Destroy(); err != nil {
		return errors.New("destroy CEF view")
	}
	viewLive = false
	window.Destroy()
	windowLive = false
	cef.Shutdown()
	cefLive = false
	return nil
}

func pumpUntil(global context.Context, phaseLimit time.Duration, waiter *lifecycleWaiter, target lifecycleMilestone) error {
	phase, cancel := context.WithTimeout(global, phaseLimit)
	defer cancel()
	mainContext := glib.MainContextDefault()
	for {
		complete, err := waiter.status(target)
		if err != nil {
			return err
		}
		if complete {
			return nil
		}

		cef.DoMessageLoopWork()
		for mainContext.Pending() {
			mainContext.Iteration(false)
		}
		select {
		case <-phase.Done():
			return fmt.Errorf("%s phase failed: %w", target, phase.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func writeSanitizedArtifactNoTest(path, content string) {
	content = urlPattern.ReplaceAllString(content, "<url>")
	content = absolutePathPattern.ReplaceAllString(content, "<path>")
	content = deviceIDPattern.ReplaceAllString(content, "<device>")
	_ = os.WriteFile(path, []byte(content), 0o600)
}
