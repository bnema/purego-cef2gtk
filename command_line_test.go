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

func TestConfigureCommandLineAddsWaylandOzonePlatform(t *testing.T) {
	commandLine := newFakeCommandLine()

	ConfigureCommandLine(commandLine, CommandLineOptions{})

	if got := commandLine.GetSwitchValue("ozone-platform"); got != "wayland" {
		t.Fatalf("ozone-platform = %q, want wayland", got)
	}
	assertForbiddenSwitchesAbsent(t, commandLine)
}

func TestConfigureCommandLineDoesNotOverrideExistingOzonePlatform(t *testing.T) {
	commandLine := newFakeCommandLine()
	commandLine.AppendSwitchWithValue("ozone-platform", "x11")

	ConfigureCommandLine(commandLine, CommandLineOptions{})

	if got := commandLine.GetSwitchValue("ozone-platform"); got != "x11" {
		t.Fatalf("ozone-platform = %q, want existing x11", got)
	}
	if len(commandLine.switches) != 1 {
		t.Fatalf("switch count = %d, want 1", len(commandLine.switches))
	}
	assertForbiddenSwitchesAbsent(t, commandLine)
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
