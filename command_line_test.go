package cef2gtk

import (
	"testing"
	"unsafe"

	"github.com/bnema/purego-cef/cef"
)

// fakeCommandLine intentionally captures switch state for unit testing.
// If command-line behavior grows, replace this with mockery-generated mocks.
type fakeCommandLine struct {
	switches map[string]string
}

var _ cef.CommandLine = (*fakeCommandLine)(nil)

func newFakeCommandLine() *fakeCommandLine {
	return &fakeCommandLine{switches: make(map[string]string)}
}

func (f *fakeCommandLine) IsValid() bool                                { return true }
func (f *fakeCommandLine) IsReadOnly() bool                             { return false }
func (f *fakeCommandLine) Copy() cef.CommandLine                        { return f }
func (f *fakeCommandLine) InitFromArgv(argc int32, argv unsafe.Pointer) {}
func (f *fakeCommandLine) InitFromString(commandLine string)            {}
func (f *fakeCommandLine) Reset()                                       { f.switches = make(map[string]string) }
func (f *fakeCommandLine) GetArgv(argv cef.StringList)                  {}
func (f *fakeCommandLine) GetCommandLineString() string                 { return "" }
func (f *fakeCommandLine) GetProgram() string                           { return "" }
func (f *fakeCommandLine) SetProgram(program string)                    {}
func (f *fakeCommandLine) HasSwitches() bool                            { return len(f.switches) > 0 }
func (f *fakeCommandLine) HasSwitch(name string) bool {
	_, ok := f.switches[name]
	return ok
}
func (f *fakeCommandLine) GetSwitchValue(name string) string               { return f.switches[name] }
func (f *fakeCommandLine) GetSwitches(switches cef.StringMap)              {}
func (f *fakeCommandLine) AppendSwitch(name string)                        { f.switches[name] = "" }
func (f *fakeCommandLine) AppendSwitchWithValue(name string, value string) { f.switches[name] = value }
func (f *fakeCommandLine) HasArguments() bool                              { return false }
func (f *fakeCommandLine) GetArguments(arguments cef.StringList)           {}
func (f *fakeCommandLine) AppendArgument(argument string)                  {}
func (f *fakeCommandLine) PrependWrapper(wrapper string)                   {}
func (f *fakeCommandLine) RemoveSwitch(name string)                        { delete(f.switches, name) }

func TestConfigureCommandLineAddsWaylandGLEGLAngleSwitchesByDefault(t *testing.T) {
	commandLine := newFakeCommandLine()

	ConfigureCommandLine(commandLine, CommandLineOptions{})

	if got := commandLine.GetSwitchValue("ozone-platform"); got != "wayland" {
		t.Fatalf("ozone-platform = %q, want wayland", got)
	}
	if got := commandLine.GetSwitchValue("use-gl"); got != "angle" {
		t.Fatalf("use-gl = %q, want angle", got)
	}
	if got := commandLine.GetSwitchValue("use-angle"); got != "gl-egl" {
		t.Fatalf("use-angle = %q, want gl-egl", got)
	}
	if commandLine.HasSwitch("enable-features") {
		t.Fatalf("enable-features should not be forced for default ANGLE GL/EGL, got %q", commandLine.GetSwitchValue("enable-features"))
	}
}

func TestConfigureCommandLineMergesExistingEnableFeaturesForExplicitVulkan(t *testing.T) {
	t.Setenv("PUREGO_CEF2GTK_ANGLE_BACKEND", "vulkan")
	commandLine := newFakeCommandLine()
	commandLine.AppendSwitchWithValue("enable-features", "ExistingFeature,Vulkan")

	ConfigureCommandLine(commandLine, CommandLineOptions{})

	want := "ExistingFeature,Vulkan,DefaultANGLEVulkan,VulkanFromANGLE"
	if got := commandLine.GetSwitchValue("enable-features"); got != want {
		t.Fatalf("enable-features = %q, want %q", got, want)
	}
}

func TestConfigureCommandLineAngleBackendOverrides(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want map[string]string
	}{
		{name: "gl egl", env: "gl-egl", want: map[string]string{"ozone-platform": "wayland", "use-gl": "angle", "use-angle": "gl-egl"}},
		{name: "vulkan", env: "vulkan", want: map[string]string{"ozone-platform": "wayland", "use-gl": "angle", "use-angle": "vulkan", "enable-features": "Vulkan,DefaultANGLEVulkan,VulkanFromANGLE"}},
		{name: "none", env: "none", want: map[string]string{"ozone-platform": "wayland", "use-angle": "none"}},
		{name: "unknown defaults to gl egl", env: "swiftshader", want: map[string]string{"ozone-platform": "wayland", "use-gl": "angle", "use-angle": "gl-egl"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PUREGO_CEF2GTK_ANGLE_BACKEND", tt.env)
			commandLine := newFakeCommandLine()

			ConfigureCommandLine(commandLine, CommandLineOptions{})

			for name, want := range tt.want {
				if got := commandLine.GetSwitchValue(name); got != want {
					t.Fatalf("%s = %q, want %q", name, got, want)
				}
			}
			if len(commandLine.switches) != len(tt.want) {
				t.Fatalf("switches = %+v, want only %+v", commandLine.switches, tt.want)
			}
		})
	}
}

func TestConfigureCommandLineExplicitAngleEnvOverridesExistingUseAngle(t *testing.T) {
	t.Setenv("PUREGO_CEF2GTK_ANGLE_BACKEND", "vulkan")
	commandLine := newFakeCommandLine()
	commandLine.AppendSwitchWithValue("use-angle", "gl-egl")
	commandLine.AppendSwitchWithValue("enable-features", "ExistingFeature")

	ConfigureCommandLine(commandLine, CommandLineOptions{})

	if got := commandLine.GetSwitchValue("use-angle"); got != "vulkan" {
		t.Fatalf("use-angle = %q, want vulkan", got)
	}
	if got := commandLine.GetSwitchValue("enable-features"); got != "ExistingFeature,Vulkan,DefaultANGLEVulkan,VulkanFromANGLE" {
		t.Fatalf("enable-features = %q, want Vulkan features appended", got)
	}
}

func TestConfigureCommandLineExplicitNoneEnvRemovesExistingUseGL(t *testing.T) {
	t.Setenv("PUREGO_CEF2GTK_ANGLE_BACKEND", "none")
	commandLine := newFakeCommandLine()
	commandLine.AppendSwitchWithValue("use-angle", "gl-egl")
	commandLine.AppendSwitchWithValue("use-gl", "angle")

	ConfigureCommandLine(commandLine, CommandLineOptions{})

	if got := commandLine.GetSwitchValue("use-angle"); got != "none" {
		t.Fatalf("use-angle = %q, want none", got)
	}
	if commandLine.HasSwitch("use-gl") {
		t.Fatalf("use-gl should be removed when ANGLE is explicitly disabled, got %q", commandLine.GetSwitchValue("use-gl"))
	}
}

func TestConfigureCommandLineExistingNoneUseAngleRemovesConflictingUseGL(t *testing.T) {
	commandLine := newFakeCommandLine()
	commandLine.AppendSwitchWithValue("use-angle", "none")
	commandLine.AppendSwitchWithValue("use-gl", "angle")

	ConfigureCommandLine(commandLine, CommandLineOptions{})

	if got := commandLine.GetSwitchValue("use-angle"); got != "none" {
		t.Fatalf("use-angle = %q, want none", got)
	}
	if commandLine.HasSwitch("use-gl") {
		t.Fatalf("use-gl should be removed when ANGLE is disabled, got %q", commandLine.GetSwitchValue("use-gl"))
	}
}

func TestConfigureCommandLineDoesNotOverrideExistingPlatformSwitches(t *testing.T) {
	tests := []struct {
		name      string
		existing  map[string]string
		want      map[string]string
		wantCount int
	}{
		{
			name:      "both switches exist with explicit vulkan",
			existing:  map[string]string{"ozone-platform": "x11", "use-angle": "vulkan"},
			want:      map[string]string{"ozone-platform": "x11", "use-angle": "vulkan", "use-gl": "angle", "enable-features": "Vulkan,DefaultANGLEVulkan,VulkanFromANGLE"},
			wantCount: 4,
		},
		{
			name:      "only ozone exists uses gl egl default",
			existing:  map[string]string{"ozone-platform": "x11"},
			want:      map[string]string{"ozone-platform": "x11", "use-gl": "angle", "use-angle": "gl-egl"},
			wantCount: 3,
		},
		{
			name:      "only vulkan angle exists gets vulkan features",
			existing:  map[string]string{"use-angle": "vulkan"},
			want:      map[string]string{"ozone-platform": "wayland", "use-angle": "vulkan", "use-gl": "angle", "enable-features": "Vulkan,DefaultANGLEVulkan,VulkanFromANGLE"},
			wantCount: 4,
		},
		{
			name:      "conflicting use gl is rewritten to angle",
			existing:  map[string]string{"use-angle": "vulkan", "use-gl": "desktop"},
			want:      map[string]string{"ozone-platform": "wayland", "use-angle": "vulkan", "use-gl": "angle", "enable-features": "Vulkan,DefaultANGLEVulkan,VulkanFromANGLE"},
			wantCount: 4,
		},
		{
			name:      "existing angle none does not force use gl angle",
			existing:  map[string]string{"use-angle": "none"},
			want:      map[string]string{"ozone-platform": "wayland", "use-angle": "none"},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commandLine := newFakeCommandLine()
			for name, value := range tt.existing {
				commandLine.AppendSwitchWithValue(name, value)
			}

			ConfigureCommandLine(commandLine, CommandLineOptions{})

			for name, want := range tt.want {
				if got := commandLine.GetSwitchValue(name); got != want {
					t.Fatalf("%s = %q, want %q", name, got, want)
				}
			}
			if len(commandLine.switches) != tt.wantCount {
				t.Fatalf("switch count = %d, want %d", len(commandLine.switches), tt.wantCount)
			}
			assertForbiddenSwitchesAbsent(t, commandLine)
		})
	}
}

func TestConfigureCommandLineNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ConfigureCommandLine panicked with nil command line: %v", r)
		}
	}()

	ConfigureCommandLine(nil, CommandLineOptions{})
}

func assertForbiddenSwitchesAbsent(t *testing.T, commandLine *fakeCommandLine) {
	t.Helper()
	for _, name := range []string{"disable-gpu", "disable-software-rasterizer", "ozone-platform-hint"} {
		if commandLine.HasSwitch(name) {
			t.Fatalf("forbidden switch %q was appended", name)
		}
	}
}
