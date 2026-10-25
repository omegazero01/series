package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configureLessonTest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "host", "lessons.json")
	ConfigureHostLessons(path)
	t.Cleanup(func() { ConfigureHostLessons("") })
	return path
}

func TestRecordHostLessonDedupesAndCounts(t *testing.T) {
	path := configureLessonTest(t)
	for i := 0; i < 2; i++ {
		RecordHostLesson("Bash", []byte(`{"command":"cd x && dir"}`), []byte("ParserError: The token '&&' is not a valid statement separator in this version."))
	}
	FlushHostLessons()
	lessons, err := loadHostLessons(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 1 {
		t.Fatalf("lessons = %d, want 1: %#v", len(lessons), lessons)
	}
	if lessons[0].Signature != "bash:andand-parsererror" || lessons[0].Count != 2 {
		t.Fatalf("unexpected lesson: %#v", lessons[0])
	}
	if !strings.Contains(lessons[0].Hint, ";") {
		t.Fatalf("hint should mention the ; replacement: %q", lessons[0].Hint)
	}
}

func TestClassifyHostLessonKnownBashFailures(t *testing.T) {
	cases := []struct {
		name string
		err  string
		sig  string
		hint string
	}{
		{"curl alias", "Invoke-WebRequest : Parameter cannot be processed because the parameter name 'm' is ambiguous. FullyQualifiedErrorId : AmbiguousParameter,Microsoft.PowerShell.Commands.InvokeWebRequestCommand", "bash:curl-alias-ambiguous", "curl.exe"},
		{"head missing", "head : The term 'head' is not recognized as the name of a cmdlet, function, script file, or operable program.", "bash:missing-command:head", "Select-Object -First"},
		{"tmp path", "curl: (23) Failed to open /tmp/dangan_home.html: No such file or directory", "bash:tmp-path", "$env:TEMP"},
		{"devnull", "The system cannot find the path specified: /dev/null", "bash:devnull", "2>$null"},
		{"cmd idiom", "Get-ChildItem : A parameter cannot be found that matches parameter name 'b'. dir /b failed.", "bash:cmd-idioms", "Get-ChildItem"},
		{"generic parser", "At line:1 char:19\n+ cd x && dir\nParserError: The token '&&' is not a valid statement separator", "bash:andand-parsererror", ";"},
	}
	for _, tc := range cases {
		lesson, ok := classifyHostLesson("Bash", []byte(`{"command":"x"}`), []byte(tc.err))
		if !ok {
			t.Fatalf("%s: not classified", tc.name)
		}
		if lesson.Signature != tc.sig {
			t.Fatalf("%s: sig = %s, want %s", tc.name, lesson.Signature, tc.sig)
		}
		if !strings.Contains(lesson.Hint, tc.hint) {
			t.Fatalf("%s: hint %q missing %q", tc.name, lesson.Hint, tc.hint)
		}
	}
}

func TestClassifyHostLessonDslFailures(t *testing.T) {
	lesson, ok := classifyHostLesson("asset_search", []byte(`{"dsl":"domain = \"x\""}`), []byte("DSL 语法错误：意外的内容 'BARE'"))
	if !ok || lesson.Signature != "dsl:syntax" || !strings.Contains(lesson.Hint, "==") {
		t.Fatalf("dsl syntax: %#v ok=%v", lesson, ok)
	}
	lesson, ok = classifyHostLesson("asset_search", []byte(`{"dsl":"path = \"/api\""}`), []byte("DSL 语法错误：未知字段: path"))
	if !ok || lesson.Signature != "dsl:unknown-field:path" || !strings.Contains(lesson.Hint, "url") {
		t.Fatalf("dsl unknown field: %#v ok=%v", lesson, ok)
	}
	lesson, ok = classifyHostLesson("asset_search", []byte(`{"types":["endpoint"]}`), []byte(`json: unknown field "types"`))
	if !ok || lesson.Signature != "asset_search:unknown-param:types" || !strings.Contains(lesson.Hint, "dsl/type") {
		t.Fatalf("unknown param: %#v ok=%v", lesson, ok)
	}
}

func TestClassifyHostLessonIgnoresNoise(t *testing.T) {
	for _, errText := range []string{
		"context deadline exceeded",
		"command timed out after 2m0s",
		"curl: (7) Failed to connect to target.test port 443: Connection refused",
		"当前角色没有 fact 读取权限",
	} {
		if lesson, ok := classifyHostLesson("Bash", []byte(`{"command":"curl https://target"}`), []byte(errText)); ok {
			t.Fatalf("noise classified as %#v", lesson)
		}
	}
}

func TestHostLessonNoteRendersAndClears(t *testing.T) {
	configureLessonTest(t)
	RecordHostLesson("Bash", nil, []byte("head : The term 'head' is not recognized as the name of a cmdlet"))
	RecordHostLesson("asset_search", nil, []byte("DSL 语法错误：意外的内容 'BARE'"))
	note := HostLessonNote()
	if !strings.Contains(note, "本机经验") || !strings.Contains(note, "Select-Object -First") || !strings.Contains(note, "asset_search") {
		t.Fatalf("unexpected note:\n%s", note)
	}
	if !strings.Contains(note, "×1") {
		t.Fatalf("note should include the hit count:\n%s", note)
	}
	ConfigureHostLessons("")
	if got := HostLessonNote(); got != "" {
		t.Fatalf("note should be empty after ConfigureHostLessons(\"\"): %q", got)
	}
}

func TestRecordHostLessonWithoutPathIsNoop(t *testing.T) {
	ConfigureHostLessons("")
	RecordHostLesson("Bash", nil, []byte("head : The term 'head' is not recognized"))
	if got := HostLessonNote(); got != "" {
		t.Fatalf("unexpected note: %q", got)
	}
}

func TestAssetSearchDescriptionCarriesHostLessons(t *testing.T) {
	configureLessonTest(t)
	RecordHostLesson("Bash", nil, []byte("head : The term 'head' is not recognized as the name of a cmdlet"))
	desc := (&ToolSet{}).assetSearch().Description()
	if !strings.Contains(desc, "本机经验") || !strings.Contains(desc, "Select-Object -First") {
		t.Fatalf("asset_search description missing the host-lesson note:\n%s", desc)
	}
}

func TestHostLessonsDebounceDoesNotWriteEveryError(t *testing.T) {
	path := configureLessonTest(t)
	for i := 0; i < 5; i++ {
		RecordHostLesson("Bash", nil, []byte("head : The term 'head' is not recognized as the name of a cmdlet"))
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("lessons file should not exist before the debounce flush")
	}
	FlushHostLessons()
	lessons, err := loadHostLessons(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 1 || lessons[0].Count != 5 {
		t.Fatalf("flush should carry the buffered count: %#v", lessons)
	}
}
