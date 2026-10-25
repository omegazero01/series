package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/memory"
)

func seedMemoryTestStore(t *testing.T) *memory.Store {
	t.Helper()
	store := memory.NewStore(t.TempDir())
	entries := []memory.Memory{
		{
			Name:        "内部系统登录接口排查记录",
			Type:        memory.TypeReference,
			Description: "登录与验证码绕过的中文经验",
			Content:     "登录接口在 /api/login，验证码可复用；参数名 userName。",
		},
		{
			Name:        "host-windows-powershell-quirks",
			Type:        memory.TypeFeedback,
			Description: "Windows PowerShell shell quirks",
			Content:     "curl is an Invoke-WebRequest alias; use curl.exe. && is unsupported.",
		},
		{
			Name:        "asset-dsl-spacing",
			Type:        memory.TypeFeedback,
			Description: "asset_search DSL spacing rules",
			Content:     "Operators may have spaces; contains and =~ mean fuzzy match.",
		},
	}
	for _, m := range entries {
		if err := store.Save(m); err != nil {
			t.Fatalf("save %s: %v", m.Name, err)
		}
	}
	return store
}

func TestMemoryCatalogSupportsChineseSubstring(t *testing.T) {
	store := seedMemoryTestStore(t)
	all, err := loadMemoryCatalog(store, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("catalog total = %d, want 3", len(all))
	}
	hit, err := loadMemoryCatalog(store, "登录接口")
	if err != nil {
		t.Fatal(err)
	}
	if len(hit) != 1 || hit[0].Name != "内部系统登录接口排查记录" {
		t.Fatalf("中文过滤失败: %#v", hit)
	}
}

func TestMemoryQueryTermsHandlesChineseAndEnglish(t *testing.T) {
	zh := memoryQueryTerms("登录接口还有什么没测")
	if len(zh) == 0 {
		t.Fatal("Chinese query produced no terms")
	}
	joined := strings.Join(zh, ",")
	for _, want := range []string{"登录", "录接", "接口"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("terms %q missing %q", joined, want)
		}
	}
	en := memoryQueryTerms("curl.exe alias")
	if strings.Join(en, ",") != "curl,exe,alias" {
		t.Fatalf("English terms = %v", en)
	}
}

func TestRankMemoriesFindsChineseOnlyMemory(t *testing.T) {
	store := seedMemoryTestStore(t)
	hits := rankMemories(store, memoryQueryTerms("登录验证码怎么绕"), 5)
	if len(hits) == 0 || hits[0].Name != "内部系统登录接口排查记录" {
		t.Fatalf("Chinese search missed the Chinese memory: %#v", hits)
	}
	en := rankMemories(store, memoryQueryTerms("curl alias"), 5)
	if len(en) == 0 || en[0].Name != "host-windows-powershell-quirks" {
		t.Fatalf("English search missed the host memory: %#v", en)
	}
}

func TestFindMemoryFallsBackToChineseTitle(t *testing.T) {
	store := seedMemoryTestStore(t)
	m, candidates, err := findMemory(store, "内部系统登录接口排查记录")
	if err != nil || len(candidates) != 0 {
		t.Fatalf("exact Chinese title lookup failed: %v candidates=%v", err, candidates)
	}
	if m.Name != "内部系统登录接口排查记录" {
		t.Fatalf("read wrong memory: %s", m.Name)
	}
	_, candidates, err = findMemory(store, "接口")
	if err != nil || len(candidates) != 0 {
		t.Fatalf("unique contains lookup should return the memory itself, got candidates=%v err=%v", candidates, err)
	}
	if _, _, err := findMemory(store, "不存在的记忆名称"); err == nil {
		t.Fatal("missing memory should return an error")
	}
}

func TestMemoryCatalogToolsExposeReadAndSaveTools(t *testing.T) {
	store := seedMemoryTestStore(t)
	tools := memoryCatalogTools(store)
	if len(tools) != 4 {
		t.Fatalf("tools = %d, want 4", len(tools))
	}
	want := map[string]bool{"ListMemories": true, "ReadMemory": true, "SearchMemories": true, "SaveMemory": true}
	for _, tool := range tools {
		if !want[tool.Name()] {
			t.Fatalf("unexpected tool %s", tool.Name())
		}
	}
	if tools := memoryCatalogTools(nil); tools != nil {
		t.Fatalf("nil store should yield no tools, got %d", len(tools))
	}
}

func TestSaveMemoryFileKeepsChineseNamesDistinct(t *testing.T) {
	store := memory.NewStore(t.TempDir())
	first := memory.Memory{Name: "纯中文记忆甲", Type: memory.TypeReference, Description: "甲", Content: "内容甲"}
	second := memory.Memory{Name: "纯中文记忆乙", Type: memory.TypeReference, Description: "乙", Content: "内容乙"}
	pathA, err := saveMemoryFile(store, first)
	if err != nil {
		t.Fatal(err)
	}
	pathB, err := saveMemoryFile(store, second)
	if err != nil {
		t.Fatal(err)
	}
	if pathA == pathB {
		t.Fatalf("pure-Chinese names collided on one file: %s", pathA)
	}
	catalog, err := loadMemoryCatalog(store, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 2 {
		t.Fatalf("catalog = %d entries, want 2", len(catalog))
	}
	for _, name := range []string{"纯中文记忆甲", "纯中文记忆乙"} {
		m, candidates, err := findMemory(store, name)
		if err != nil || len(candidates) != 0 || m.Name != name {
			t.Fatalf("findMemory(%q) = %+v candidates=%v err=%v", name, m, candidates, err)
		}
	}
	// Same name writes/updates in place instead of creating a second file.
	updated := first
	updated.Content = "内容甲已更新"
	pathA2, err := saveMemoryFile(store, updated)
	if err != nil {
		t.Fatal(err)
	}
	if pathA2 != pathA {
		t.Fatalf("same-name update moved the file: %s -> %s", pathA, pathA2)
	}
	catalog, _ = loadMemoryCatalog(store, "")
	if len(catalog) != 2 {
		t.Fatalf("catalog after update = %d entries, want 2", len(catalog))
	}
	idx, err := os.ReadFile(filepath.Join(store.Dir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"纯中文记忆甲", "纯中文记忆乙"} {
		if !strings.Contains(string(idx), name) {
			t.Fatalf("MEMORY.md missing %q:\n%s", name, idx)
		}
	}
}
