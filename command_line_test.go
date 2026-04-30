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

func TestConfigureCommandLineAddsWaylandGLAngleSwitches(t *testing.T) {
	commandLine := newFakeCommandLine()

	ConfigureCommandLine(commandLine, CommandLineOptions{})

	if got := commandLine.GetSwitchValue("ozone-platform"); got != "wayland" {
		t.Fatalf("ozone-platform = %q, want wayland", got)
	}
	if got := commandLine.GetSwitchValue("use-angle"); got != "gl-egl" {
		t.Fatalf("use-angle = %q, want gl-egl", got)
	}
	assertForbiddenSwitchesAbsent(t, commandLine)
}

func TestConfigureCommandLineDoesNotOverrideExistingPlatformSwitches(t *testing.T) {
	tests := []struct {
		name      string
		existing  map[string]string
		want      map[string]string
		wantCount int
	}{
		{
			name:      "both switches exist",
			existing:  map[string]string{"ozone-platform": "x11", "use-angle": "vulkan"},
			want:      map[string]string{"ozone-platform": "x11", "use-angle": "vulkan"},
			wantCount: 2,
		},
		{
			name:      "only ozone exists",
			existing:  map[string]string{"ozone-platform": "x11"},
			want:      map[string]string{"ozone-platform": "x11", "use-angle": "gl-egl"},
			wantCount: 2,
		},
		{
			name:      "only angle exists",
			existing:  map[string]string{"use-angle": "vulkan"},
			want:      map[string]string{"ozone-platform": "wayland", "use-angle": "vulkan"},
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
	for _, name := range []string{"enable-features", "disable-gpu", "disable-software-rasterizer", "use-gl", "ozone-platform-hint"} {
		if commandLine.HasSwitch(name) {
			t.Fatalf("forbidden switch %q was appended", name)
		}
	}
}
