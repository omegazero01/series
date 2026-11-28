package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// hostLesson is one automatically distilled tool/environment mistake. Lessons
// are host-scoped (they describe this machine + this toolchain), deduplicated by
// signature and surfaced in the Bash / asset_search tool descriptions on the
// next run. Raw tool input/output is deliberately not stored: errors often echo
// tokens, URLs or credentials.
type hostLesson struct {
	Signature string    `json:"signature"`
	Tool      string    `json:"tool"`
	Hint      string    `json:"hint"`
	Count     int       `json:"count"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

type hostLessonFile struct {
	Version int          `json:"version"`
	Lessons []hostLesson `json:"lessons"`
}

const (
	hostLessonVersion  = 1
	hostLessonMaxItems = 80
	hostLessonMaxNote  = 700
)

var (
	hostLessonPathMu  sync.RWMutex
	hostLessonPath    string
	hostLessonWriteMu sync.Mutex
)

// hostLessonBuf debounces disk writes: errors often arrive in bursts (a retry
// loop can produce dozens), and one atomic rename per error is wasted IO.
const hostLessonFlushDelay = 2 * time.Second

var hostLessonBuf struct {
	mu      sync.Mutex
	pending map[string]hostLesson
	timer   *time.Timer
}

// ConfigureHostLessons points the recorder and the tool-description note at a
// JSON file (normally <data>/host/lessons.json). An empty path disables both.
// Pending debounced counts are flushed to the previous path first.
func ConfigureHostLessons(path string) {
	FlushHostLessons()
	hostLessonPathMu.Lock()
	hostLessonPath = strings.TrimSpace(path)
	hostLessonPathMu.Unlock()
}

func currentHostLessonPath() string {
	hostLessonPathMu.RLock()
	defer hostLessonPathMu.RUnlock()
	return hostLessonPath
}

// HostLessonNote renders the most frequent lessons for tool descriptions. It is
// empty when no lessons have been recorded yet, so a fresh install sees no
// addendum and the note disappears again after ConfigureHostLessons("").
func HostLessonNote() string {
	path := currentHostLessonPath()
	if path == "" {
		return ""
	}
	lessons, err := loadHostLessons(path)
	if err != nil {
		lessons = nil
	}
	// Overlay not-yet-flushed counts so a build right after a burst of errors
	// still sees the current numbers.
	lessons = mergeHostLessonLists(lessons, snapshotHostLessonPending())
	if len(lessons) == 0 {
		return ""
	}
	sort.SliceStable(lessons, func(i, j int) bool {
		if lessons[i].Count != lessons[j].Count {
			return lessons[i].Count > lessons[j].Count
		}
		return lessons[i].LastSeen.After(lessons[j].LastSeen)
	})
	var b strings.Builder
	b.WriteString("\n\n【本机经验（根据历史报错自动沉淀）】")
	added := 0
	for _, l := range lessons {
		if added >= 6 {
			break
		}
		line := "\n- [" + l.Tool + " ×" + itoa(l.Count) + "] " + l.Hint
		if len([]rune(b.String()))+len([]rune(line)) > hostLessonMaxNote {
			break
		}
		b.WriteString(line)
		added++
	}
	if added == 0 {
		return ""
	}
	return b.String()
}

// RecordHostLesson classifies one failed tool call and merges it into the lesson
// store. It is best-effort and intentionally does not touch the disk: counts are
// buffered in memory and flushed at most once per hostLessonFlushDelay, so a
// retry loop cannot turn into one atomic write per error.
func RecordHostLesson(tool string, input, result []byte) {
	if currentHostLessonPath() == "" {
		return
	}
	lesson, ok := classifyHostLesson(tool, input, result)
	if !ok {
		return
	}
	now := time.Now().UTC()
	hostLessonBuf.mu.Lock()
	if hostLessonBuf.pending == nil {
		hostLessonBuf.pending = map[string]hostLesson{}
	}
	mergeHostLessonInto(hostLessonBuf.pending, lesson, now)
	if hostLessonBuf.timer == nil {
		hostLessonBuf.timer = time.AfterFunc(hostLessonFlushDelay, FlushHostLessons)
	}
	hostLessonBuf.mu.Unlock()
}

// FlushHostLessons writes buffered counts to disk. It is safe to call with no
// pending lessons and is called by ConfigureHostLessons and the debounce timer.
func FlushHostLessons() {
	hostLessonBuf.mu.Lock()
	pending := hostLessonBuf.pending
	hostLessonBuf.pending = nil
	if hostLessonBuf.timer != nil {
		hostLessonBuf.timer.Stop()
		hostLessonBuf.timer = nil
	}
	hostLessonBuf.mu.Unlock()
	path := currentHostLessonPath()
	if path == "" || len(pending) == 0 {
		return
	}
	hostLessonWriteMu.Lock()
	defer hostLessonWriteMu.Unlock()
	lessons, _ := loadHostLessons(path)
	lessons = mergeHostLessonLists(lessons, mapValues(pending))
	if len(lessons) > hostLessonMaxItems {
		sort.SliceStable(lessons, func(i, j int) bool {
			if lessons[i].Count != lessons[j].Count {
				return lessons[i].Count > lessons[j].Count
			}
			return lessons[i].LastSeen.After(lessons[j].LastSeen)
		})
		lessons = lessons[:hostLessonMaxItems]
	}
	_ = saveHostLessons(path, lessons)
}

func snapshotHostLessonPending() []hostLesson {
	hostLessonBuf.mu.Lock()
	defer hostLessonBuf.mu.Unlock()
	return mapValues(hostLessonBuf.pending)
}

func mapValues(m map[string]hostLesson) []hostLesson {
	out := make([]hostLesson, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func mergeHostLessonInto(pending map[string]hostLesson, lesson hostLesson, now time.Time) {
	if existing, ok := pending[lesson.Signature]; ok {
		existing.Count++
		existing.LastSeen = now
		if lesson.Hint != "" {
			existing.Hint = lesson.Hint
		}
		pending[lesson.Signature] = existing
		return
	}
	lesson.Count = 1
	lesson.FirstSeen = now
	lesson.LastSeen = now
	pending[lesson.Signature] = lesson
}

// mergeHostLessonLists folds pending counts into the on-disk list without
// modifying either input; used by both the flush path and the note overlay.
func mergeHostLessonLists(base, pending []hostLesson) []hostLesson {
	out := append([]hostLesson(nil), base...)
	for _, p := range pending {
		found := false
		for i := range out {
			if out[i].Signature != p.Signature {
				continue
			}
			out[i].Count += p.Count
			if p.LastSeen.After(out[i].LastSeen) {
				out[i].LastSeen = p.LastSeen
			}
			if p.Hint != "" {
				out[i].Hint = p.Hint
			}
			found = true
			break
		}
		if !found {
			out = append(out, p)
		}
	}
	return out
}

func loadHostLessons(path string) ([]hostLesson, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f hostLessonFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return f.Lessons, nil
}

func saveHostLessons(path string, lessons []hostLesson) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(hostLessonFile{Version: hostLessonVersion, Lessons: lessons}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var (
	bashAmpRegex     = regexp.MustCompile(`not a valid statement separator|'&&'`)
	bashCmdRegex     = regexp.MustCompile(`'([^']+)'`)
	bashUnknownField = regexp.MustCompile(`未知字段[:：]?\s*([A-Za-z0-9_]+)`)
	jsonUnknownField = regexp.MustCompile(`unknown field "([^"]+)"`)
)

// classifyHostLesson turns an error blob into a stable signature + actionable
// hint. Only host/toolchain classes are recorded; timeouts and target-side
// failures are intentionally ignored so the note does not fill with noise.
func classifyHostLesson(tool string, input, result []byte) (hostLesson, bool) {
	text := string(result)
	lower := strings.ToLower(text)
	tool = strings.TrimSpace(tool)

	switch {
	case strings.Contains(lower, "ambiguousparameter") && strings.Contains(lower, "invoke-webrequest"):
		return hostLesson{Signature: "bash:curl-alias-ambiguous", Tool: "Bash",
			Hint: "curl 在 PowerShell 里是 Invoke-WebRequest 的别名，-m 等参数会歧义；HTTP 请求改用 curl.exe（或 Invoke-WebRequest -TimeoutSec/-UseBasicParsing）。"}, true
	case bashAmpRegex.MatchString(text):
		return hostLesson{Signature: "bash:andand-parsererror", Tool: "Bash",
			Hint: "Windows PowerShell 5.1 不支持 && / ||；多步命令用 ; 分隔，条件判断用 if (...) { ... }。"}, true
	case strings.Contains(lower, "/dev/null"):
		return hostLesson{Signature: "bash:devnull", Tool: "Bash",
			Hint: "PowerShell 没有 /dev/null；丢弃输出用 2>$null 或 | Out-Null。"}, true
	case strings.Contains(lower, "2>nul") || strings.Contains(lower, "dir /b") || strings.Contains(lower, "cd /d"):
		return hostLesson{Signature: "bash:cmd-idioms", Tool: "Bash",
			Hint: "不要用 cmd 的 dir /b、cd /d、2>nul；对应的 PowerShell 写法是 Get-ChildItem、Set-Location、2>$null。"}, true
	case strings.Contains(text, "/tmp/") && (strings.Contains(lower, "no such file") || strings.Contains(lower, "cannot find") || strings.Contains(lower, "failed to open")):
		return hostLesson{Signature: "bash:tmp-path", Tool: "Bash",
			Hint: "Windows 没有 /tmp；临时文件写到 $env:TEMP\\文件名。"}, true
	case strings.Contains(lower, "commandnotfoundexception") || strings.Contains(lower, "is not recognized as the name of a cmdlet"):
		name := ""
		if m := bashCmdRegex.FindStringSubmatch(text); len(m) == 2 {
			name = strings.ToLower(strings.TrimSpace(m[1]))
		}
		hint := missingCommandHint(name)
		sig := "bash:missing-command"
		if name != "" {
			sig += ":" + name
		}
		return hostLesson{Signature: sig, Tool: "Bash", Hint: hint}, true
	case strings.Contains(text, "ParserError") || strings.Contains(lower, "parsererror"):
		return hostLesson{Signature: "bash:parsererror", Tool: "Bash",
			Hint: "命令被 PowerShell 解析失败：检查引号/括号，把复杂逻辑写成 .ps1 或 Python 脚本文件再执行，不要原样重试。"}, true
	case tool == "asset_search" && strings.Contains(text, "DSL 语法错误"):
		if m := bashUnknownField.FindStringSubmatch(text); len(m) == 2 {
			return hostLesson{Signature: "dsl:unknown-field:" + strings.ToLower(m[1]), Tool: "asset_search",
				Hint: "DSL 未知字段 " + m[1] + "；可用 domain/root_domain/ip/url/page_title/icp/service_name/app_name/method/service_type/technology/port/status_code/company_id/task_id/approval_state；路径查询用 url。"}, true
		}
		return hostLesson{Signature: "dsl:syntax", Tool: "asset_search",
			Hint: "DSL 报语法错误：运算符两侧可有空格；= 模糊、== 精确、contains/=~ 同 =；AND/OR 组合，路径用 url，参数名是 type。"}, true
	case tool == "asset_search" && strings.Contains(lower, "unknown field"):
		if m := jsonUnknownField.FindStringSubmatch(text); len(m) == 2 {
			return hostLesson{Signature: "asset_search:unknown-param:" + strings.ToLower(m[1]), Tool: "asset_search",
				Hint: "asset_search 参数 " + m[1] + " 不存在；可用 dsl/type/untested/limit/offset（type 不是 types）。"}, true
		}
	}
	return hostLesson{}, false
}

func missingCommandHint(name string) string {
	switch name {
	case "head":
		return "PowerShell 没有 head；取前 N 行用 Select-Object -First N。"
	case "tail":
		return "PowerShell 没有 tail；取后 N 行用 Select-Object -Last N，跟踪用 Get-Content -Wait。"
	case "grep":
		return "PowerShell 没有 grep；用 Select-String（或 findstr）。"
	case "wc":
		return "PowerShell 没有 wc；统计行数用 (Get-Content f).Count 或 Measure-Object -Line。"
	case "export":
		return "PowerShell 没有 export；设置环境变量用 $env:NAME=\"值\"。"
	case "python3":
		return "Windows 上通常是 python 而不是 python3。"
	case "touch":
		return "PowerShell 没有 touch；新建空文件用 New-Item -ItemType File。"
	case "cat":
		return "PowerShell 的 cat 是 Get-Content 别名；建议显式写 Get-Content。"
	case "sudo":
		return "PowerShell 没有 sudo；需要管理员权限时用管理员身份启动，或 Start-Process -Verb RunAs。"
	case "chmod":
		return "PowerShell 没有 chmod；Windows 上用 icacls 设置 ACL。"
	}
	if name != "" {
		return "命令 " + name + " 在 Windows PowerShell 不存在；先用 Get-Command 确认，或改用 python/.ps1 脚本。"
	}
	return "命令在当前 PowerShell 中不存在；先用 Get-Command 确认，或改用 python/.ps1 脚本。"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
