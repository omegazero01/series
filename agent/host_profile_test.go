package agent

import (
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
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

func TestBashPromptOverrideOnlyOnWindows(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		if _, ok := bashPromptOverrideFor(goos); ok {
			t.Fatalf("%s must keep the SDK prompt", goos)
		}
	}
	prompt, ok := bashPromptOverrideFor("windows")
	if !ok {
		t.Fatal("windows must override the SDK prompt")
	}
	if prompt != "" {
		t.Fatalf("windows override drops the SDK text entirely, got %q", prompt)
	}
}

func TestOverrideToolPromptDropsSDKText(t *testing.T) {
	base := actool.NewBash()
	// Guard: if the SDK ever stops advertising &&/heredocs the override can go.
	if p := base.Prompt(); !strings.Contains(p, "heredocs") || !strings.Contains(p, "&&") {
		t.Fatalf("SDK Bash prompt changed; re-check whether the override is still needed: %q", p)
	}
	out := OverrideToolPrompt(base, "")
	if got := out.Prompt(); got != "" {
		t.Fatalf("Prompt() = %q, want empty", got)
	}
	if out.Name() != base.Name() || out.Description() != base.Description() {
		t.Fatal("override must delegate everything except Prompt()")
	}
}

func TestRegistrySchemaDropsSDKPromptAfterOverride(t *testing.T) {
	base := actool.NewBash()
	before := actool.NewRegistry(base).Schemas()[0].Description
	if !strings.Contains(before, "chain steps with") {
		t.Fatalf("SDK schema no longer carries the generic prompt: %q", before)
	}
	after := actool.NewRegistry(OverrideToolPrompt(base, "")).Schemas()[0].Description
	if strings.Contains(after, "chain steps with") || strings.Contains(after, "heredoc") {
		t.Fatalf("override must remove the SDK prompt from the rendered schema:\n%s", after)
	}
	if !strings.Contains(after, "Executes a shell command") {
		t.Fatalf("base description must survive the override:\n%s", after)
	}
}
