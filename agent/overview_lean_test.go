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

func TestLeanFactSelectionRanksTriggerThenFrontier(t *testing.T) {
	full := leanFixtureFull()
	full["open_intents"] = []map[string]any{
		{"id": int64(10), "summary": "frontier 10", "state": "open"},
		{"id": int64(11), "summary": "frontier 11", "state": "open"},
	}
	full["running_intents"] = []map[string]any{}
	full["recent_facts"] = []map[string]any{
		{"id": int64(1), "summary": "recent only", "from_intent": int64(999)},
		{"id": int64(2), "summary": "frontier 10 fact", "from_intent": int64(10)},
		{"id": int64(3), "summary": "trigger 20 fact", "from_intent": int64(20)},
		{"id": int64(4), "summary": "frontier 11 fact", "from_intent": int64(11)},
		{"id": int64(5), "summary": "trigger 21 fact", "from_intent": int64(21)},
	}
	limits := DefaultOverviewConfig().Lean
	limits.RecentFacts = 3
	focus := &OverviewFocus{TriggerIntentIDs: []int64{20, 21}}
	lean := leanGraphOverviewData(full, LeanOptions{Limits: limits, Focus: focus})
	facts := lean["recent_facts"].([]map[string]any)
	if len(facts) != 3 {
		t.Fatalf("facts=%d want 3", len(facts))
	}
	if facts[0]["id"] != int64(5) || facts[1]["id"] != int64(3) || facts[2]["id"] != int64(4) {
		t.Fatalf("order=%v want [5 3 4]", []any{facts[0]["id"], facts[1]["id"], facts[2]["id"]})
	}
	if _, leaked := facts[0]["_sources"]; leaked {
		t.Fatal("_sources leaked into lean output")
	}
}

func TestLeanFactSelectionUsesConfiguredLimit(t *testing.T) {
	full := leanFixtureFull()
	full["recent_facts"] = leanFixtureRows(25, func(i int) map[string]any {
		return map[string]any{"id": int64(400 + i), "summary": "fact", "from_intent": int64(999)}
	})
	for _, want := range []int{8, 12, 20} {
		limits := DefaultOverviewConfig().Lean
		limits.RecentFacts = want
		lean := leanGraphOverviewData(full, LeanOptions{Limits: limits})
		if got := len(lean["recent_facts"].([]map[string]any)); got != want {
			t.Fatalf("recent_facts=%d want %d", got, want)
		}
	}
}

func TestFactSelectionEnabled(t *testing.T) {
	t.Setenv("SERIES_PLANNER_FACT_SELECTION", "off")
	if factSelectionEnabled() {
		t.Fatal("off should disable fact selection")
	}
	t.Setenv("SERIES_PLANNER_FACT_SELECTION", "on")
	if !factSelectionEnabled() {
		t.Fatal("on should enable fact selection")
	}
	t.Setenv("SERIES_PLANNER_FACT_SELECTION", "")
	if !factSelectionEnabled() {
		t.Fatal("empty should default to enabled for lean")
	}
}

func TestLeanFactRowsPreservesLegacyOrderWithoutFocus(t *testing.T) {
	full := leanFixtureFull()
	full["recent_facts"] = []map[string]any{
		{"id": int64(1), "summary": "first", "from_intent": int64(999)},
		{"id": int64(2), "summary": "second", "from_intent": int64(10)},
		{"id": int64(3), "summary": "third", "from_intent": int64(999)},
	}
	limits := DefaultOverviewConfig().Lean
	limits.RecentFacts = 2
	lean := leanGraphOverviewData(full, LeanOptions{Limits: limits})
	facts := lean["recent_facts"].([]map[string]any)
	if len(facts) != 2 || facts[0]["id"] != int64(1) || facts[1]["id"] != int64(2) {
		t.Fatalf("legacy order=%v", []any{facts[0]["id"], facts[1]["id"]})
	}
}

func TestLeanFactSelectionRanksOneHopRelations(t *testing.T) {
	full := leanFixtureFull()
	full["open_intents"] = []map[string]any{
		{"id": int64(10), "summary": "frontier 10", "state": "open"},
	}
	full["running_intents"] = []map[string]any{}
	full["recent_facts"] = []map[string]any{
		{"id": int64(1), "summary": "direct trigger", "from_intent": int64(20)},
		{"id": int64(2), "summary": "one-hop trigger", "_onehop": []int64{20}},
		{"id": int64(3), "summary": "direct frontier", "from_intent": int64(10)},
		{"id": int64(4), "summary": "one-hop frontier", "_onehop": []int64{10}},
		{"id": int64(5), "summary": "plain recent", "from_intent": int64(999)},
	}
	limits := DefaultOverviewConfig().Lean
	limits.RecentFacts = 4
	focus := &OverviewFocus{TriggerIntentIDs: []int64{20}}
	lean := leanGraphOverviewData(full, LeanOptions{Limits: limits, Focus: focus})
	facts := lean["recent_facts"].([]map[string]any)
	if len(facts) != 4 {
		t.Fatalf("facts=%d want 4", len(facts))
	}
	want := []int64{1, 2, 3, 4}
	for i, id := range want {
		if facts[i]["id"] != id {
			t.Fatalf("order=%v want %v", testFactIDs(facts), want)
		}
	}
}

func TestLeanFactSelectionUsesPriorityAndRecency(t *testing.T) {
	full := leanFixtureFull()
	full["open_intents"] = []map[string]any{
		{"id": int64(10), "summary": "frontier 10", "state": "open"},
	}
	full["running_intents"] = []map[string]any{}
	// Same direct frontier source: importance breaks the tie, then recency.
	full["recent_facts"] = []map[string]any{
		{"id": int64(1), "summary": "high priority", "from_intent": int64(10), "_priority": 10},
		{"id": int64(2), "summary": "low priority", "from_intent": int64(10), "_priority": 0},
	}
	limits := DefaultOverviewConfig().Lean
	limits.RecentFacts = 1
	lean := leanGraphOverviewData(full, LeanOptions{Limits: limits, Focus: &OverviewFocus{}})
	facts := lean["recent_facts"].([]map[string]any)
	if len(facts) != 1 || facts[0]["id"] != int64(1) {
		t.Fatalf("priority order=%v want [1]", testFactIDs(facts))
	}

	full["recent_facts"] = []map[string]any{
		{"id": int64(1), "summary": "old", "from_intent": int64(999)},
		{"id": int64(2), "summary": "new", "from_intent": int64(999)},
	}
	limits.RecentFacts = 1
	lean = leanGraphOverviewData(full, LeanOptions{Limits: limits, Focus: &OverviewFocus{TriggerIntentIDs: []int64{20}}})
	facts = lean["recent_facts"].([]map[string]any)
	if len(facts) != 1 || facts[0]["id"] != int64(2) {
		t.Fatalf("recency order=%v want [2]", testFactIDs(facts))
	}
}

func testFactIDs(rows []map[string]any) []int64 {
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		if id, ok := structuredInt64(row["id"]); ok {
			out = append(out, id)
		}
	}
	return out
}

func TestPositiveTriggerIntentIDsExcludesCancelled(t *testing.T) {
	got := positiveTriggerIntentIDs([]TriggerEvent{
		{Kind: "done", IntentID: 7},
		{Kind: "cancelled", IntentID: 7},
		{Kind: "finding", IntentID: 8},
		{Kind: "hint", IntentID: 9},
		{Kind: "done", IntentID: 7},
	})
	if len(got) != 2 || got[0] != 7 || got[1] != 8 {
		t.Fatalf("focus=%v want [7 8]", got)
	}
}

func TestOverviewConfigDefaultsAndValidation(t *testing.T) {
	def := DefaultOverviewConfig()
	if def.Full.BudgetRunes != plannerOverviewRunes || def.Lean.BudgetRunes != plannerLeanOverviewRunes {
		t.Fatalf("budgets=%+v", def)
	}
	if def.Full.RecentFacts != 20 || def.Lean.RecentFacts != 8 {
		t.Fatalf("facts=%+v", def)
	}
	if err := ValidateOverviewConfig(def); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	bad := def
	bad.Lean.RecentFacts = 21
	if err := ValidateOverviewConfig(bad); err == nil {
		t.Fatal("expected facts > max to be rejected")
	}
	bad = def
	bad.Full.BudgetRunes = 1999
	if err := ValidateOverviewConfig(bad); err == nil {
		t.Fatal("expected budget < min to be rejected")
	}
	partial := OverviewConfig{Lean: OverviewLimits{RecentFacts: 0}}
	if err := ValidateOverviewConfig(partial); err != nil {
		t.Fatalf("zero should mean default: %v", err)
	}
	norm := partial.Normalize()
	if norm.Lean.RecentFacts != 8 || norm.Full.RecentFacts != 20 {
		t.Fatalf("normalized=%+v", norm)
	}
}

func TestLeanGraphOverviewUsesCustomLimits(t *testing.T) {
	full := leanFixtureFull()
	limits := DefaultOverviewConfig().Lean
	limits.Goals = 4
	limits.RecentFacts = 2
	limits.Findings = 1
	limits.RecentDoneIntents = 3
	limits.Hints = 4
	limits.ColdDigests = 1
	lean := leanGraphOverviewData(full, LeanOptions{Limits: limits})
	if got := len(lean["goals"].([]map[string]any)); got != 4 {
		t.Fatalf("goals=%d want 4", got)
	}
	if got := len(lean["recent_facts"].([]map[string]any)); got != 2 {
		t.Fatalf("facts=%d want 2", got)
	}
	if got := len(lean["finding_list"].([]map[string]any)); got != 1 {
		t.Fatalf("findings=%d want 1", got)
	}
	if got := len(lean["recent_done_intents"].([]map[string]any)); got != 3 {
		t.Fatalf("done=%d want 3", got)
	}
	if got := len(lean["hints"].([]map[string]any)); got != 4 {
		t.Fatalf("hints=%d want 4", got)
	}
	if got := len(lean["cold_digests"].([]map[string]any)); got != 1 {
		t.Fatalf("digests=%d want 1", got)
	}
	if got := len(lean["open_intents"].([]map[string]any)); got != 25 {
		t.Fatalf("frontier=%d want all 25", got)
	}
	if got := len(lean["running_intents"].([]map[string]any)); got != 3 {
		t.Fatalf("running=%d want all 3", got)
	}
}

func TestTrimGraphOverviewDataKeepsFrontier(t *testing.T) {
	full := leanFixtureFull()
	limits := DefaultOverviewConfig().Full
	limits.Goals = 4
	limits.RecentFacts = 3
	limits.Findings = 2
	limits.RecentDoneIntents = 4
	limits.Hints = 5
	limits.ColdDigests = 2
	trimmed := trimGraphOverviewData(full, limits)
	if got := len(trimmed["goals"].([]map[string]any)); got != 4 {
		t.Fatalf("goals=%d want 4", got)
	}
	if got := len(trimmed["recent_facts"].([]map[string]any)); got != 3 {
		t.Fatalf("facts=%d want 3", got)
	}
	if got := len(trimmed["open_intents"].([]map[string]any)); got != 25 {
		t.Fatalf("frontier=%d want all 25", got)
	}
	if got := len(trimmed["running_intents"].([]map[string]any)); got != 3 {
		t.Fatalf("running=%d want all 3", got)
	}
	if got := len(full["recent_facts"].([]map[string]any)); got != 12 {
		t.Fatalf("source facts mutated=%d", got)
	}
}

func TestRenderGraphOverviewUsesCustomLimits(t *testing.T) {
	full := leanFixtureFull()
	cfg := DefaultOverviewConfig()
	cfg.Lean.RecentFacts = 2
	cfg.Lean.Findings = 1
	rendered := renderGraphOverview(full, OverviewLean, OverviewRenderOptions{Config: cfg})
	start := strings.Index(rendered, "<graph_overview>")
	end := strings.Index(rendered, "</graph_overview>")
	if start < 0 || end <= start {
		t.Fatalf("missing graph_overview tags: %q", rendered)
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(rendered[start+len("<graph_overview>"):end]), &view); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if got := len(view["recent_facts"].([]any)); got > 2 {
		t.Fatalf("rendered facts=%d want <=2", got)
	}
	if got := len(view["finding_list"].([]any)); got > 1 {
		t.Fatalf("rendered findings=%d want <=1", got)
	}
}
