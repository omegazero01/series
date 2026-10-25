package agent

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	actool "github.com/Autumn-27/norma/tool"

	"github.com/Autumn-27/norma/memory"
)

// memoryUsageHint is appended to the worker system prompt when a memory store is
// configured. The memory tools are self-describing, but the old behaviour showed
// the model almost never reached for them on its own: a short usage hint plus a
// record-your-own-hiccup instruction is the cheapest way to turn the store into
// something that actually gets read.
const memoryUsageHint = "\n\n【长期记忆】可用 ListMemories 查看记忆目录（中文标题也能直接看到）、SearchMemories 做中英文检索、ReadMemory 按名称读取正文、RecallMemory 按相关性回忆；动手前先看目录，命中相关经验先读取。遇到可复用的本机/工具坑并解决后，用 RecordMemory 写一条（名称可用中文；含接口名、参数名等英文锚点更利于以后召回）；标 pinned:true（或 type=feedback / 名称以 host- 开头）的记忆会作为常驻记忆每次注入。"

// memoryCatalogTools exposes the memory directory to the worker. norma already
// ships RecallMemory/RecordMemory, but retrieval there is English-token only,
// MEMORY.md is not injected, and RecordMemory's slug() collapses pure-Chinese
// names onto the same file. These tools replace that surface without forking
// the SDK: RecallMemory/RecordMemory keep the familiar names, List/Read/Search
// add directory browsing and Chinese matching.
func memoryCatalogTools(store *memory.Store) []actool.CoreTool {
	if store == nil {
		return nil
	}
	return []actool.CoreTool{
		listMemoriesTool(store),
		readMemoryTool(store),
		searchMemoriesTool(store),
		recallMemoryTool(store),
		recordMemoryTool(store),
	}
}

func listMemoriesTool(store *memory.Store) actool.CoreTool {
	return readTool("ListMemories", "列出长期记忆目录（标题、类型、一句话描述、保存时间）。中文标题也直接可见；query 可做名称/描述/正文子串过滤。拿到名称后用 ReadMemory 读正文，或用 SearchMemories 检索。", obj(map[string]any{
		"query": str("可选过滤词，支持中文子串；留空列出全部"),
		"limit": intp("默认50，1..100"),
	}, "query"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
		var a struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return actool.Errorf(err.Error()), nil
		}
		if a.Limit <= 0 {
			a.Limit = 50
		}
		if a.Limit > 100 {
			a.Limit = 100
		}
		mems, err := loadMemoryCatalog(store, a.Query)
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		now := time.Now()
		total := len(mems)
		if len(mems) > a.Limit {
			mems = mems[:a.Limit]
		}
		rows := make([]map[string]any, 0, len(mems))
		for _, m := range mems {
			rows = append(rows, map[string]any{
				"name":        m.Name,
				"type":        string(m.Type),
				"description": m.Description,
				"saved":       memory.Age(m, now),
			})
		}
		out := map[string]any{"memories": rows, "total": total, "has_more": total > len(rows)}
		b, err := json.Marshal(out)
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		return actool.Text(clipMemoryText(string(b), readOutputBudget(ctx))), nil
	})
}

func readMemoryTool(store *memory.Store) actool.CoreTool {
	return readTool("ReadMemory", "按名称读取一条长期记忆的完整正文；名称来自 ListMemories 或 RecallMemory。找不到时返回相近候选。", obj(map[string]any{
		"name": str("记忆名称或 slug"),
	}, "name"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
		var a struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return actool.Errorf(err.Error()), nil
		}
		m, candidates, err := findMemory(store, strings.TrimSpace(a.Name))
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		if len(candidates) > 0 {
			return actool.Errorf(fmt.Sprintf("未找到记忆 %q；相近候选：%s。可用 ListMemories 查看完整目录。", a.Name, strings.Join(candidates, "、"))), nil
		}
		return actool.Text(clipMemoryText(renderOneMemory(m), readOutputBudget(ctx))), nil
	})
}

func searchMemoriesTool(store *memory.Store) actool.CoreTool {
	return readTool("SearchMemories", "在长期记忆中做中英文检索并返回最相关记忆的正文。中文按双字滑窗、英文按词匹配，纯中文问题也能命中；需要目录用 ListMemories，按名读取用 ReadMemory。", obj(map[string]any{
		"query": str("检索词，中英文均可"),
		"limit": intp("默认5，1..10"),
	}, "query"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
		var a struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return actool.Errorf(err.Error()), nil
		}
		if a.Limit <= 0 {
			a.Limit = 5
		}
		if a.Limit > 10 {
			a.Limit = 10
		}
		terms := memoryQueryTerms(a.Query)
		if len(terms) == 0 {
			return actool.Text("检索词为空；可用 ListMemories 查看记忆目录。"), nil
		}
		hits := rankMemories(store, terms, a.Limit)
		if len(hits) == 0 {
			return actool.Text("没有匹配的长期记忆；可用 ListMemories 查看目录。"), nil
		}
		return actool.Text(clipMemoryText(renderMemoryHits(hits), readOutputBudget(ctx))), nil
	})
}

func recallMemoryTool(store *memory.Store) actool.CoreTool {
	return readTool("RecallMemory", "按相关性回忆长期记忆（中英文均可，中文按双字滑窗），返回最相关记忆的正文；与 SearchMemories 同源，保留这个名字兼容旧习惯。", obj(map[string]any{
		"query": str("要回忆的内容"),
	}, "query"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
		var a struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return actool.Errorf(err.Error()), nil
		}
		terms := memoryQueryTerms(a.Query)
		if len(terms) == 0 {
			return actool.Text("No relevant memories found."), nil
		}
		hits := rankMemories(store, terms, 5)
		if len(hits) == 0 {
			return actool.Text("No relevant memories found."), nil
		}
		return actool.Text(clipMemoryText(renderMemoryHits(hits), readOutputBudget(ctx))), nil
	})
}

func recordMemoryTool(store *memory.Store) actool.CoreTool {
	return writeTool("RecordMemory", "写入/更新一条长期记忆（安全文件名：纯中文标题使用名称哈希，多个中文名不会互相覆盖；写完重建 MEMORY.md）。名称可用中文；含接口名、参数名等英文锚点更利于以后召回。type 取 user/feedback/project/reference（默认 reference）；pinned=true（或 type=feedback、名称以 host- 开头）会被当作常驻记忆，每次注入 worker 系统提示。", obj(map[string]any{
		"name":        str("记忆标题；同名写入视为更新"),
		"description": str("一句话摘要"),
		"type":        str("user | feedback | project | reference，默认 reference"),
		"content":     str("要记住的事实正文（markdown）"),
		"pinned":      map[string]any{"type": "boolean", "description": "是否常驻注入，默认 false"},
	}, "name", "content"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
		var a struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Type        string `json:"type"`
			Content     string `json:"content"`
			Pinned      bool   `json:"pinned"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return actool.Errorf(err.Error()), nil
		}
		if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Content) == "" {
			return actool.Errorf("name 和 content 不能为空"), nil
		}
		typ := strings.TrimSpace(a.Type)
		if typ == "" {
			typ = string(memory.TypeReference)
		}
		switch memory.Type(typ) {
		case memory.TypeUser, memory.TypeFeedback, memory.TypeProject, memory.TypeReference:
		default:
			return actool.Errorf("type 需为 user/feedback/project/reference"), nil
		}
		m := memory.Memory{Name: a.Name, Description: a.Description, Type: memory.Type(typ), Content: a.Content}
		path, err := saveMemoryFile(store, m, a.Pinned)
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		return actool.Text(fmt.Sprintf("Saved memory %q (%s).", a.Name, filepath.Base(path))), nil
	})
}

// saveMemoryFile writes a memory without norma's slug() collapse: non-ASCII-only
// names get a deterministic hash-based filename, and a name collision with a
// different memory gets a hash suffix instead of overwriting it. The SDK's
// MEMORY.md index is rebuilt in the same format so other readers stay correct.
func saveMemoryFile(store *memory.Store, m memory.Memory, pinned bool) (string, error) {
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Content) == "" {
		return "", fmt.Errorf("memory: name and content are required")
	}
	slug := memorySlugName(m.Name)
	if slug == "" {
		slug = "memory-" + memoryNameHash(m.Name)
	}
	path := filepath.Join(store.Dir, slug+".md")
	if existing, _, err := readMemoryAt(path); err == nil && !strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(m.Name)) {
		slug = slug + "-" + memoryNameHash(m.Name)
		path = filepath.Join(store.Dir, slug+".md")
	}
	if err := os.MkdirAll(store.Dir, 0o755); err != nil {
		return "", fmt.Errorf("memory dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderMemoryFile(m, pinned)), 0o644); err != nil {
		return "", fmt.Errorf("write memory: %w", err)
	}
	if err := rebuildMemoryIndex(store); err != nil {
		return "", fmt.Errorf("rebuild memory index: %w", err)
	}
	return path, nil
}

func memoryNameHash(name string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(name)))
	return fmt.Sprintf("%x", sum[:4])
}

func renderMemoryFile(m memory.Memory, pinned bool) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", m.Name)
	fmt.Fprintf(&b, "description: %s\n", m.Description)
	if m.Type != "" {
		fmt.Fprintf(&b, "type: %s\n", m.Type)
	}
	if pinned {
		b.WriteString("pinned: true\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(m.Content, "\n"))
	b.WriteString("\n")
	return b.String()
}

func readMemoryAt(path string) (memory.Memory, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return memory.Memory{}, false, err
	}
	m, pinned := parseMemoryFileText(string(raw))
	m.Path = path
	return m, pinned, nil
}

// parseMemoryFileText mirrors norma memory.parseFrontmatter for the subset the
// tool writes; using our own reader keeps the file format in one place. The
// second result is the pinned flag, which the SDK parser intentionally ignores.
func parseMemoryFileText(s string) (memory.Memory, bool) {
	var m memory.Memory
	pinned := false
	if strings.HasPrefix(s, "---") {
		rest := strings.TrimPrefix(s, "---")
		rest = strings.TrimLeft(rest, "\r\n")
		if i := strings.Index(rest, "\n---"); i >= 0 {
			head := rest[:i]
			body := strings.TrimLeft(rest[i+len("\n---"):], "-")
			m.Content = strings.TrimLeft(body, "\r\n")
			for _, line := range strings.Split(head, "\n") {
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				k = strings.TrimSpace(k)
				v = strings.TrimSpace(v)
				switch k {
				case "name":
					m.Name = v
				case "description":
					m.Description = v
				case "type":
					m.Type = memory.Type(v)
				case "pinned":
					pinned = strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes")
				}
			}
		}
	} else {
		m.Content = s
	}
	return m, pinned
}

func rebuildMemoryIndex(store *memory.Store) error {
	mems, err := store.Scan()
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Memory\n\n")
	if len(mems) == 0 {
		b.WriteString("(no memories yet)\n")
	} else {
		for _, m := range mems {
			fmt.Fprintf(&b, "- [%s](%s) — %s\n", m.Name, filepath.Base(m.Path), m.Description)
		}
	}
	return os.WriteFile(filepath.Join(store.Dir, "MEMORY.md"), []byte(b.String()), 0o644)
}

// loadMemoryCatalog scans the store newest-first and keeps entries whose
// name/description/body contains query (case-insensitive substring; Chinese
// works without tokenisation).
func loadMemoryCatalog(store *memory.Store, query string) ([]memory.Memory, error) {
	mems, err := store.Scan()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return mems, nil
	}
	var out []memory.Memory
	for _, m := range mems {
		hay := strings.ToLower(m.Name + "\n" + m.Description + "\n" + string(m.Type) + "\n" + m.Content)
		if strings.Contains(hay, q) {
			out = append(out, m)
		}
	}
	return out, nil
}

// memoryPinnedFor reports whether a memory is always injected: explicit
// `pinned: true` frontmatter, type=feedback, or a host- name prefix. The SDK
// parser ignores the custom frontmatter key, so this stays compatible with
// everything else that reads the same files.
func memoryPinnedFor(m memory.Memory) bool {
	if _, pinned, err := readMemoryAt(m.Path); err == nil && pinned {
		return true
	}
	if m.Type == memory.TypeFeedback {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(m.Name)), "host-")
}

// HostPinnedNote renders the always-injected memory list (name + one-line
// description) for the worker system prompt. Bodies stay behind
// ReadMemory/RecallMemory so the per-run cost stays bounded.
func HostPinnedNote(store *memory.Store) string {
	if store == nil {
		return ""
	}
	mems, err := store.Scan()
	if err != nil {
		return ""
	}
	var pinned []memory.Memory
	for _, m := range mems {
		if memoryPinnedFor(m) {
			pinned = append(pinned, m)
		}
	}
	if len(pinned) == 0 {
		return ""
	}
	sort.SliceStable(pinned, func(i, j int) bool { return pinned[i].ModTime.After(pinned[j].ModTime) })
	var b strings.Builder
	b.WriteString("\n\n【常驻记忆（pinned / feedback / host-，每次注入）】")
	n := 0
	for _, m := range pinned {
		if n >= 8 {
			break
		}
		line := "\n- " + m.Name
		if strings.TrimSpace(m.Description) != "" {
			line += "：" + m.Description
		}
		if len([]rune(b.String()))+len([]rune(line)) > 900 {
			break
		}
		b.WriteString(line)
		n++
	}
	if n == 0 {
		return ""
	}
	b.WriteString("\n（完整正文用 ReadMemory 或 RecallMemory 读取。）")
	return hostClipRunes(b.String(), 950)
}

// memoryRelevantNote injects a few task-relevant memories at run start, using
// the same Chinese-aware ranking as RecallMemory. Pinned memories are already
// covered by HostPinnedNote, so they are skipped here.
func memoryRelevantNote(store *memory.Store, query string, limit int) string {
	if store == nil || limit <= 0 {
		return ""
	}
	terms := memoryQueryTerms(query)
	if len(terms) == 0 {
		return ""
	}
	hits := rankMemories(store, terms, limit+4)
	var picked []memory.Memory
	for _, m := range hits {
		if memoryPinnedFor(m) {
			continue
		}
		picked = append(picked, m)
		if len(picked) >= limit {
			break
		}
	}
	if len(picked) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【相关长期记忆（按当前意图检索）】")
	n := 0
	for _, m := range picked {
		line := "\n- " + m.Name
		if strings.TrimSpace(m.Description) != "" {
			line += "：" + m.Description
		}
		if len([]rune(b.String()))+len([]rune(line)) > 700 {
			break
		}
		b.WriteString(line)
		n++
	}
	if n == 0 {
		return ""
	}
	b.WriteString("\n（完整正文用 ReadMemory 读取。）")
	return b.String()
}

// findMemory reads by exact name/slug first, then falls back to a
// case-insensitive exact/contains scan so a Chinese title copied from
// ListMemories is always openable. It returns nearby candidates instead of a
// hard miss.
func findMemory(store *memory.Store, name string) (memory.Memory, []string, error) {
	if name == "" {
		return memory.Memory{}, nil, fmt.Errorf("name 不能为空")
	}
	// norma's slug() strips every non-ASCII run, so a purely-Chinese name maps
	// to an empty slug and Read() would happily return an unrelated ".md" file
	// saved under another Chinese name. Only trust Read() when the returned
	// memory actually matches the requested name or slug; otherwise fall back to
	// the full scan below.
	if m, err := store.Read(name); err == nil && m.Name != "" {
		if strings.EqualFold(strings.TrimSpace(m.Name), name) || strings.EqualFold(memorySlugName(m.Name), strings.ToLower(name)) {
			return m, nil, nil
		}
	}
	mems, err := store.Scan()
	if err != nil {
		return memory.Memory{}, nil, err
	}
	low := strings.ToLower(name)
	var exact []memory.Memory
	var contains []memory.Memory
	for _, m := range mems {
		switch {
		case strings.EqualFold(m.Name, name) || strings.EqualFold(memorySlugName(m.Name), low):
			exact = append(exact, m)
		case strings.Contains(strings.ToLower(m.Name), low):
			contains = append(contains, m)
		}
	}
	pick := exact
	if len(pick) == 0 {
		pick = contains
	}
	if len(pick) == 1 {
		return pick[0], nil, nil
	}
	if len(pick) > 1 {
		names := make([]string, 0, min(5, len(pick)))
		for _, m := range pick {
			if len(names) >= 5 {
				break
			}
			names = append(names, m.Name)
		}
		return memory.Memory{}, names, nil
	}
	return memory.Memory{}, nil, fmt.Errorf("未找到名称 %q 的记忆；可用 ListMemories 查看目录", name)
}

// memoryQueryTerms turns a query into matchable terms: ASCII words of length >=2
// plus Han bigrams, so a pure-Chinese query produces usable terms instead of the
// empty token list norma's ASCII-only tokenizer would yield.
func memoryQueryTerms(query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	seen := map[string]bool{}
	var terms []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		terms = append(terms, s)
	}
	for _, w := range memoryWordRe.FindAllString(q, -1) {
		add(w)
	}
	runes := []rune(q)
	for i := 0; i+1 < len(runes); i++ {
		if unicode.Is(unicode.Han, runes[i]) && unicode.Is(unicode.Han, runes[i+1]) {
			add(string(runes[i : i+2]))
		}
	}
	if len(terms) == 0 && len(runes) > 0 {
		add(string(runes)) // single-character or punctuation-only query
	}
	return terms
}

func rankMemories(store *memory.Store, terms []string, limit int) []memory.Memory {
	mems, err := store.Scan()
	if err != nil || len(mems) == 0 || len(terms) == 0 {
		return nil
	}
	type scored struct {
		m memory.Memory
		s int
	}
	var ranked []scored
	for _, m := range mems {
		name := strings.ToLower(m.Name + " " + string(m.Type))
		desc := strings.ToLower(m.Description)
		body := strings.ToLower(m.Content)
		score := 0
		for _, t := range terms {
			if strings.Contains(name, t) {
				score += 3
			}
			if strings.Contains(desc, t) {
				score += 2
			}
			if strings.Contains(body, t) {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{m: m, s: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].s != ranked[j].s {
			return ranked[i].s > ranked[j].s
		}
		return ranked[i].m.ModTime.After(ranked[j].m.ModTime)
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]memory.Memory, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, r.m)
	}
	return out
}

func renderOneMemory(m memory.Memory) string {
	now := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n类型：%s\n保存：%s\n\n%s", m.Name, m.Type, memory.Age(m, now), strings.TrimSpace(m.Content))
	return b.String()
}

func renderMemoryHits(mems []memory.Memory) string {
	now := time.Now()
	var b strings.Builder
	b.WriteString("匹配到的长期记忆：")
	for _, m := range mems {
		fmt.Fprintf(&b, "\n\n--- %s（类型 %s，保存于 %s）---\n%s", m.Name, m.Type, memory.Age(m, now), strings.TrimSpace(m.Content))
	}
	return b.String()
}

func clipMemoryText(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n…（已截断；请用 ReadMemory 逐条读取完整内容）"
}

var memoryWordRe = regexp.MustCompile(`[a-z0-9_]{2,}`)

var memorySlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// memorySlugName mirrors norma memory.slug() so a caller can pass either the
// display name or the slug when opening a memory.
func memorySlugName(name string) string {
	return strings.Trim(memorySlugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
}
