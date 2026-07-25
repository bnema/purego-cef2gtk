package main

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveURLPreservesHTTPSEndingInHTML(t *testing.T) {
	const raw = "https://example.com/input-playground.html"

	got, err := resolveURL(raw)
	if err != nil {
		t.Fatalf("resolveURL(%q): %v", raw, err)
	}
	if got != raw {
		t.Fatalf("resolveURL(%q) = %q, want %q", raw, got, raw)
	}
}

func TestResolveURLPreservesOrdinaryHTTPSURL(t *testing.T) {
	const raw = "https://example.com/docs?topic=input#mouse"

	got, err := resolveURL(raw)
	if err != nil {
		t.Fatalf("resolveURL(%q): %v", raw, err)
	}
	if got != raw {
		t.Fatalf("resolveURL(%q) = %q, want %q", raw, got, raw)
	}
}

func TestResolveURLPreservesFileURL(t *testing.T) {
	const raw = "file:///tmp/input%20playground.html"

	got, err := resolveURL(raw)
	if err != nil {
		t.Fatalf("resolveURL(%q): %v", raw, err)
	}
	if got != raw {
		t.Fatalf("resolveURL(%q) = %q, want %q", raw, got, raw)
	}
}

func TestResolveURLConvertsAbsolutePathAndEscapesSpaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input playground.html")
	want := (&url.URL{Scheme: "file", Path: path}).String()

	got, err := resolveURL(path)
	if err != nil {
		t.Fatalf("resolveURL(%q): %v", path, err)
	}
	if got != want {
		t.Fatalf("resolveURL(%q) = %q, want %q", path, got, want)
	}
}

func TestResolveURLConvertsRelativeHTMLPathFromControlledWorkingDirectory(t *testing.T) {
	originalWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWorkingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	workingDirectory := t.TempDir()
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatalf("change working directory: %v", err)
	}

	const relativePath = "relative playground.html"
	want := (&url.URL{Scheme: "file", Path: filepath.Join(workingDirectory, relativePath)}).String()
	got, err := resolveURL(relativePath)
	if err != nil {
		t.Fatalf("resolveURL(%q): %v", relativePath, err)
	}
	if got != want {
		t.Fatalf("resolveURL(%q) = %q, want %q", relativePath, got, want)
	}
}
