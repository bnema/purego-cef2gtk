package cef2gtk

import (
	"testing"

	"github.com/bnema/purego-cef/cef"
)

func TestConfigureBrowserSettingsAppliesWindowlessFrameRate(t *testing.T) {
	settings := cef.NewBrowserSettings()

	ConfigureBrowserSettings(&settings, BrowserSettingsOptions{WindowlessFrameRate: 144})

	if settings.WindowlessFrameRate != 144 {
		t.Fatalf("WindowlessFrameRate = %d, want 144", settings.WindowlessFrameRate)
	}
}

func TestConfigureBrowserSettingsLeavesFrameRateUnchangedWhenUnset(t *testing.T) {
	settings := cef.NewBrowserSettings()
	settings.WindowlessFrameRate = 72

	ConfigureBrowserSettings(&settings, BrowserSettingsOptions{})

	if settings.WindowlessFrameRate != 72 {
		t.Fatalf("WindowlessFrameRate = %d, want existing 72", settings.WindowlessFrameRate)
	}
}

func TestConfigureBrowserSettingsNilSafe(t *testing.T) {
	ConfigureBrowserSettings(nil, BrowserSettingsOptions{WindowlessFrameRate: 144})
}
