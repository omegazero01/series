package agent

import (
	"strings"
	"testing"
)

func TestBuildHostEnvironmentNoteWindows(t *testing.T) {
	note := buildHostEnvironmentNote(hostProfile{
		GOOS:      "windows",
		PSVersion: "5.1.26100.9711",
		CurlAlias: "Invoke-WebRequest",
		CurlExe:   `C:\Windows\System32\curl.exe`,
		TempDir:   `C:\Users\tester\AppData\Local\Temp`,
	})
	if note == "" {
		t.Fatal("windows note is empty")
	}
	for _, want := range []string{
		"Windows PowerShell",
		"5.1.26100.9711",
		"&&",
		"$env:TEMP",
		"curl.exe",
		"Invoke-WebRequest",
		"不要原样重试",
	} {
		if !strings.Contains(note, want) {
			t.Fatalf("note missing %q:\n%s", want, note)
		}
	}
}

func TestBuildHostEnvironmentNoteOmitsUnknownPowershellVersion(t *testing.T) {
	note := buildHostEnvironmentNote(hostProfile{GOOS: "windows", CurlExe: `C:\curl.exe`})
	if strings.Contains(note, "当前版本") {
		t.Fatalf("note should omit an unknown PowerShell version:\n%s", note)
	}
	if !strings.Contains(note, "curl.exe") {
		t.Fatalf("note lost the curl.exe hint:\n%s", note)
	}
}

func TestBuildHostEnvironmentNoteNonWindowsIsEmpty(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		if note := buildHostEnvironmentNote(hostProfile{GOOS: goos}); note != "" {
			t.Fatalf("%s note should be empty:\n%s", goos, note)
		}
	}
}
