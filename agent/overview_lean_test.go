package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func leanFixtureRows(n int, makeRow func(int) map[string]any) []map[string]any {
	rows := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, makeRow(i))
	}
	return rows
}

func leanFixtureFull() map[string]any {
	return map[string]any{
		"task": map[string]any{"description": "fixture task", "goal": "fixture goal"},
		"goals": leanFixtureRows(25, func(i int) map[string]any {
			return map[string]any{"id": int64(i + 1), "state": "open", "text": strings.Repeat("目标", 50)}
		}),
		"open_intents": leanFixtureRows(25, func(i int) map[string]any {
			return map[string]any{
				"id":        int64(100 + i),
				"summary":   strings.Repeat("待办方向", 30),
				"state":     "open",
				"asset_ids": []int64{1, 2, 3, 4, 5, 6, 7},
				"parents":   []int64{11, 12, 13, 14, 15, 16, 17, 18},
				"yields":    []int64{21, 22, 23, 24, 25, 26, 27, 28, 29},
			}
		}),
		"running_intents": leanFixtureRows(3, func(i int) map[string]any {
			return map[string]any{"id": int64(200 + i), "summary": "running", "state": "running"}
		}),
		"recent_done_intents": leanFixtureRows(9, func(i int) map[string]any {
			return map[string]any{
				"id":      int64(300 + i),
				"summary": "done",
				"state":   "done",
				"yields":  []int64{1, 2, 3, 4, 5, 6},
			}
		}),
		"recent_facts": leanFixtureRows(12, func(i int) map[string]any {
			return map[string]any{
				"id":          int64(400 + i),
				"summary":     strings.Repeat("事实", 20),
				"confidence":  "observed",
				"from_intent": int64(300),
			}
		}),
		"finding_list": leanFixtureRows(6, func(i int) map[string]any {
			return map[string]any{"id": int64(500 + i), "summary": "finding", "from_intent": int64(300)}
		}),
		"hints": leanFixtureRows(15, func(i int) map[string]any {
			return map[string]any{
				"id":           int64(600 + i),
				"state":        "active",
				"text":         "hint text",
				"traffic_refs": []any{map[string]any{"traffic_id": "t-1"}},
			}
		}),
		"cold_digests": leanFixtureRows(5, func(i int) map[string]any {
			return map[string]any{
				"id":              int64(700 + i),
				"body":            strings.Repeat("冷摘要", 1000),
				"body_is_summary": true,
				"member_count":    12,
			}
		}),
		"related_tasks": []map[string]any{
			{"source_task_id": int64(9), "recent_facts": leanFixtureRows(20, func(i int) map[string]any {
				return map[string]any{"id": int64(i), "summary": strings.Repeat("related", 100)}
			})},
		},
		"coverage": map[string]any{
			"denominator": 26, "tested": 11, "pct": 0.42, "host_count": 4,
			"by_type": []any{map[string]any{"type": "subdomain", "total": 6}},
			"note":    "coverage note",
		},
		"frontier_open":  25,
		"facts":          120,
		"findings_total": 6,
	}
}

func TestLeanGraphOverviewProjection(t *testing.T) {
	full := leanFixtureFull()
	before, _ := json.Marshal(full)
	lean := leanGraphOverviewData(full)
	after, _ := json.Marshal(full)
	if string(before) != string(after) {
		t.Fatal("lean projection mutated source")
	}

	if got := len(lean["goals"].([]map[string]any)); got != 20 {
		t.Fatalf("goals=%d want 20", got)
	}
	open := lean["open_intents"].([]map[string]any)
	if len(open) != 25 {
		t.Fatalf("open intents=%d want all 25", len(open))
	}
	first := open[0]
	if got := len(first["asset_ids"].([]any)); got != 5 {
		t.Fatalf("asset ids=%d want 5", got)
	}
	if got := len(first["parents"].([]int64)); got != 5 {
		t.Fatalf("parents=%d want 5", got)
	}
	if first["parents_count"] != 8 || first["yields_count"] != 9 {
		t.Fatalf("lineage counts=%v", first)
	}
	if _, exists := first["yields"]; exists {
		t.Fatalf("open intent should not carry full yields: %v", first)
	}
	if got := len(lean["running_intents"].([]map[string]any)); got != 3 {
		t.Fatalf("running=%d want 3", got)
	}
	done := lean["recent_done_intents"].([]map[string]any)
	if len(done) != 5 {
		t.Fatalf("done=%d want 5", len(done))
	}
	if got := len(done[0]["yields"].([]int64)); got != 5 {
		t.Fatalf("done yields=%d want 5", got)
	}
	if got := len(lean["recent_facts"].([]map[string]any)); got != 8 {
		t.Fatalf("facts=%d want 8", got)
	}
	if got := len(lean["finding_list"].([]map[string]any)); got != 3 {
		t.Fatalf("findings=%d want 3", got)
	}
	hints := lean["hints"].([]map[string]any)
	if len(hints) != 10 {
		t.Fatalf("hints=%d want 10", len(hints))
	}
	if _, exists := hints[0]["traffic_refs"]; exists {
		t.Fatalf("lean hint kept traffic_refs: %v", hints[0])
	}
	if got := len(lean["cold_digests"].([]map[string]any)); got != 3 {
		t.Fatalf("digests=%d want 3", got)
	}
	if _, exists := lean["related_tasks"]; exists {
		t.Fatal("lean overview should omit related_tasks bodies")
	}
	cov := lean["coverage"].(map[string]any)
	if _, exists := cov["by_type"]; exists {
		t.Fatalf("coverage kept by_type: %v", cov)
	}
	if _, exists := cov["note"]; exists {
		t.Fatalf("coverage kept note: %v", cov)
	}
}

func TestLeanGraphOverviewKeepsUnavailable(t *testing.T) {
	full := map[string]any{
		"unavailable": map[string]any{"hints": "database query failed"},
		"partial":     true,
	}
	lean := leanGraphOverviewData(full)
	if lean["partial"] != true {
		t.Fatal("partial lost")
	}
	unavailable, ok := lean["unavailable"].(map[string]any)
	if !ok || unavailable["hints"] != "database query failed" {
		t.Fatalf("unavailable=%v", lean["unavailable"])
	}
}

func TestRenderGraphOverviewLeanBudgetAndSections(t *testing.T) {
	full := leanFixtureFull()
	full["goals"] = full["goals"].([]map[string]any)[:5]
	full["open_intents"] = full["open_intents"].([]map[string]any)[:8]
	rendered := renderGraphOverview(full, OverviewLean)
	t.Logf("lean render runes=%d source_open=%d", utf8.RuneCountInString(rendered), len(leanGraphOverviewData(full)["open_intents"].([]map[string]any)))
	if runes := utf8.RuneCountInString(rendered); runes > plannerLeanOverviewRunes {
		t.Fatalf("lean render=%d runes, want <= %d", runes, plannerLeanOverviewRunes)
	}
	start := strings.Index(rendered, "<graph_overview>")
	end := strings.Index(rendered, "</graph_overview>")
	if start < 0 || end <= start {
		t.Fatalf("missing graph_overview tags: %q", rendered)
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(rendered[start+len("<graph_overview>"):end]), &view); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	sections, ok := view["sections"].(map[string]any)
	if !ok {
		t.Fatalf("missing sections: %v", view)
	}
	t.Logf("open section=%v", sections["open_intents"])
	facts, ok := sections["recent_facts"].(map[string]any)
	if !ok || facts["has_more"] != true {
		t.Fatalf("recent_facts section=%v", sections["recent_facts"])
	}
	if facts["read"] == nil {
		t.Fatalf("recent_facts missing read pointer: %v", facts)
	}
	if got := len(view["open_intents"].([]any)); got != 8 {
		t.Fatalf("render open intents=%d want 8", got)
	}
}

func TestPlannerOverviewProfileDefault(t *testing.T) {
	p := &Planner{}
	if got := p.overviewProfile(); got != OverviewFull {
		t.Fatalf("nil resolver=%q want full", got)
	}
	p.SetOverviewProfile(func() OverviewProfile { return OverviewLean })
	if got := p.overviewProfile(); got != OverviewLean {
		t.Fatalf("resolver=%q want lean", got)
	}
	p.SetOverviewProfile(func() OverviewProfile { return "unknown" })
	if got := p.overviewProfile(); got != OverviewFull {
		t.Fatalf("unknown resolver=%q want full", got)
	}
}
