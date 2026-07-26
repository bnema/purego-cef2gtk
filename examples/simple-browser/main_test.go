package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

func TestResolveURLUsesHTTPSFallbackForHostLikeHTMLPath(t *testing.T) {
	const raw = "example.com/index.html"
	const want = "https://example.com/index.html"

	got, err := resolveURL(raw)
	if err != nil {
		t.Fatalf("resolveURL(%q): %v", raw, err)
	}
	if got != want {
		t.Fatalf("resolveURL(%q) = %q, want %q", raw, got, want)
	}
}

func TestResolveURLUsesHTTPSFallbackForDriveLetterHTMLPath(t *testing.T) {
	const raw = "C:/tmp/index.html"
	const want = "https://C:/tmp/index.html"

	got, err := resolveURL(raw)
	if err != nil {
		t.Fatalf("resolveURL(%q): %v", raw, err)
	}
	if got != want {
		t.Fatalf("resolveURL(%q) = %q, want %q", raw, got, want)
	}
}

func TestResolveURLRejectsNonHTMLDriveLetterPathAsUnsupportedScheme(t *testing.T) {
	const raw = "C:/tmp/readme.txt"

	_, err := resolveURL(raw)
	const wantErr = `unsupported scheme "c"`
	if err == nil || err.Error() != wantErr {
		t.Fatalf("resolveURL(%q) error = %v, want %q", raw, err, wantErr)
	}
}

func TestResolveURLValidatesFileURLs(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{name: "localhost", raw: "file://localhost/tmp/index.html", want: "file://localhost/tmp/index.html"},
		{name: "localhost case insensitive", raw: "file://LOCALHOST/tmp/index.html", want: "file://LOCALHOST/tmp/index.html"},
		{name: "empty path", raw: "file://localhost", wantErr: "path"},
		{name: "opaque", raw: "file:index.html", wantErr: "opaque"},
		{name: "remote host", raw: "file://example.com/tmp/index.html", wantErr: "host"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveURL(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveURL(%q) error = %v, want error containing %q", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveURL(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("resolveURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestInputPlaygroundPinsSortableJSIntegrity(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "tests", "manual", "input-playground.html"))
	if err != nil {
		t.Fatalf("read input playground: %v", err)
	}
	const script = `<script src="https://cdn.jsdelivr.net/npm/sortablejs@1.15.6/Sortable.min.js" integrity="sha384-HZZ/fukV+9G8gwTNjN7zQDG0Sp7MsZy5DDN6VfY3Be7V9dvQpEpR2jF2HlyFUUjU" crossorigin="anonymous"></script>`
	if !strings.Contains(string(fixture), script) {
		t.Fatalf("input playground does not contain pinned SortableJS script with verified SRI and anonymous CORS")
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
