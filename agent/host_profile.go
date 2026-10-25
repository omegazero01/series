package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// hostProfile is a read-only snapshot of the machine the agent runs on. It is
// detected once per process and folded into an always-visible note attached to
// the Bash tool description, so the model does not have to recall a memory to
// know which shell it is actually talking to.
type hostProfile struct {
	GOOS      string
	PSVersion string // Windows PowerShell version, e.g. "5.1.26100.9711"
	CurlAlias string // e.g. "Invoke-WebRequest" when `curl` is a PowerShell alias
	CurlExe   string // resolved path of the real curl.exe, when present
	TempDir   string
}

var (
	hostProfileOnce sync.Once
	hostProfileNote string
)

// HostEnvironmentNote returns the per-host execution-environment note for the
// Bash tool description. It is empty on non-Windows hosts: the note exists to
// bridge the gap between the model's bash/cmd habits and Windows PowerShell.
func HostEnvironmentNote() string {
	hostProfileOnce.Do(func() {
		hostProfileNote = buildHostEnvironmentNote(detectHostProfile())
	})
	return hostProfileNote
}

// detectHostProfile runs a small set of read-only probes. Every probe is
// best-effort: a missing command or a timeout leaves the corresponding field
// empty and the note simply omits that detail.
func detectHostProfile() hostProfile {
	p := hostProfile{GOOS: runtime.GOOS, TempDir: os.TempDir()}
	if runtime.GOOS != "windows" {
		return p
	}
	p.CurlExe = hostLookPath("curl.exe")
	p.PSVersion = hostPowerShellProbe(`$PSVersionTable.PSVersion.ToString()`)
	p.CurlAlias = hostPowerShellProbe(`(Get-Alias curl -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty Definition)`)
	if p.CurlAlias == "curl.exe" {
		// A broken/empty alias lookup must not turn into a misleading note.
		p.CurlAlias = ""
	}
	return p
}

func hostLookPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

// hostPowerShellProbe runs one read-only PowerShell command with a short
// timeout so a hung profile can never delay tool assembly.
func hostPowerShellProbe(script string) string {
	if hostLookPath("powershell") == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// buildHostEnvironmentNote renders the note from an already-detected profile.
// Split out so tests can render deterministic Windows/macOS snapshots without
// running any probe.
func buildHostEnvironmentNote(p hostProfile) string {
	if p.GOOS != "windows" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【本机运行环境｜每轮固定注入】\n")
	b.WriteString("- 系统：Windows；Bash 工具实际由 Windows PowerShell 执行（-NoProfile -NonInteractive -Command），不是 bash，也不是 cmd。")
	if p.PSVersion != "" {
		fmt.Fprintf(&b, " 当前版本 %s。", p.PSVersion)
	}
	b.WriteString("\n- 不要用 && 或 ||；多步命令用 ; 分隔。不要用 dir /b、cd /d、2>nul 这类 cmd 写法。")
	b.WriteString("\n- 没有 head/tail/grep/wc/export：用 Select-Object -First/-Last、Select-String、Measure-Object、$env:NAME=\"值\"。")
	b.WriteString("\n- 临时文件不要写 /tmp/，用 $env:TEMP\\文件名。")
	if p.CurlExe != "" {
		b.WriteString("\n- HTTP 请求用 curl.exe")
		if p.CurlAlias != "" {
			fmt.Fprintf(&b, "（curl 是 %s 的别名：-m 等参数会歧义，且不会加载 .curlrc 默认 UA）", p.CurlAlias)
		}
		b.WriteString("；复杂 HTTP 先写 .ps1 或 Python 脚本文件再执行。")
	} else {
		b.WriteString("\n- HTTP 不要用 curl 别名；用 Invoke-WebRequest -TimeoutSec/-UseBasicParsing，或先写脚本。")
	}
	b.WriteString("\n- 命令报语法/参数错误时，先按错误改用 PowerShell 写法，不要原样重试。")
	return hostClipRunes(b.String(), 900)
}

func hostClipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
