package agent

import "sort"

// leanGraphOverviewData is a pure, deliberately lossy projection of the
// graph_overview snapshot used for the planner's per-round pre-injected
// situation block. It never mutates the input and never widens authorization:
// it only trims fields and list windows from data already filtered by
// graphOverviewData.
//
// The exploration graph remains the source of truth. Anything omitted here is
// still available through context_search / context_get under the existing task
// scope.
func leanGraphOverviewData(full map[string]any, opts ...LeanOptions) map[string]any {
	if full == nil {
		return map[string]any{}
	}
	cfg := DefaultOverviewConfig().Lean
	var focus *OverviewFocus
	if len(opts) > 0 {
		cfg = normalizeOverviewLimits(opts[0].Limits, cfg)
		focus = opts[0].Focus
	}
	frontierIDs := overviewIDSet(full["open_intents"], full["running_intents"])
	out := make(map[string]any, 18)
	for _, key := range []string{
		"task", "asset_approval_counts", "unavailable", "partial", "error",
		"frontier_open", "facts", "findings_total", "done_intents_total",
		"done_intents_in_window", "finding_list_truncated", "cold_digests_has_more",
	} {
		if v, ok := full[key]; ok {
			out[key] = v
		}
	}
	if task, ok := full["task"].(map[string]any); ok {
		out["task"] = map[string]any{
			"description": task["description"],
			"goal":        task["goal"],
		}
	}
	if rows, ok := overviewRows(full["goals"]); ok {
		out["goals"] = leanGoalRows(rows, cfg.Goals)
	}
	// Frontier is never truncated: dropping an open/running row makes the
	// planner think a direction does not exist and duplicate work. Only fields
	// are compressed here.
	if rows, ok := overviewRows(full["open_intents"]); ok {
		out["open_intents"] = leanIntentRows(rows, 0, false)
	}
	if rows, ok := overviewRows(full["running_intents"]); ok {
		out["running_intents"] = leanIntentRows(rows, 0, false)
	}
	if rows, ok := overviewRows(full["recent_done_intents"]); ok {
		out["recent_done_intents"] = leanIntentRows(rows, cfg.RecentDoneIntents, true)
	}
	if rows, ok := overviewRows(full["recent_facts"]); ok {
		out["recent_facts"] = leanFactRows(rows, cfg.RecentFacts, focus, frontierIDs)
	}
	if rows, ok := overviewRows(full["finding_list"]); ok {
		out["finding_list"] = leanFindingRows(rows, cfg.Findings)
	}
	if rows, ok := overviewRows(full["hints"]); ok {
		out["hints"] = leanHintRows(rows, cfg.Hints)
	}
	if rows, ok := overviewRows(full["cold_digests"]); ok {
		out["cold_digests"] = leanDigestRows(rows, cfg.ColdDigests)
	}
	if cov, ok := full["coverage"].(map[string]any); ok {
		out["coverage"] = leanCoverage(cov)
	}
	// related_tasks is intentionally omitted: its nested per-source facts and
	// hints are the most expensive part of the overview. The section metadata
	// produced by boundedGraphOverview tells the planner it was not provided,
	// and context_search(task_id=...) remains available.
	return out
}

func overviewRows(value any) ([]map[string]any, bool) {
	switch v := value.(type) {
	case []map[string]any:
		return v, true
	case []any:
		rows := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if row, ok := item.(map[string]any); ok {
				rows = append(rows, row)
			}
		}
		return rows, true
	default:
		return nil, false
	}
}

func capOverviewRows(rows []map[string]any, limit int) []map[string]any {
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func leanText(value any, limit int) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

func leanGoalRows(rows []map[string]any, limit int) []map[string]any {
	rows = capOverviewRows(rows, limit)
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id":    row["id"],
			"state": row["state"],
			"text":  leanText(row["text"], 500),
		})
	}
	return out
}

// leanIntentRows keeps the causal skeleton (parents ids + counts) but drops
// long summaries and full downstream arrays. terminal=true keeps the first few
// downstream ids for recent_done_intents so the planner can see what a finished
// direction produced.
func leanIntentRows(rows []map[string]any, limit int, terminal bool) []map[string]any {
	rows = capOverviewRows(rows, limit)
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		m := map[string]any{
			"id":      row["id"],
			"summary": leanText(row["summary"], 300),
			"state":   row["state"],
		}
		for _, key := range []string{"inherited", "source_task_id"} {
			if v, ok := row[key]; ok {
				m[key] = v
			}
		}
		if v, ok := row["asset_ids"]; ok {
			if ids, ok := leanAnyList(v, 5); ok {
				m["asset_ids"] = ids
			}
		}
		if parents, total := leanInt64List(row["parents"], 5); total > 0 {
			m["parents"] = parents
			m["parents_count"] = total
		}
		if yields, total := leanInt64List(row["yields"], 5); total > 0 {
			if terminal {
				m["yields"] = yields
			}
			m["yields_count"] = total
		}
		out = append(out, m)
	}
	return out
}

func leanFactRows(rows []map[string]any, limit int, focus *OverviewFocus, frontierIDs map[int64]struct{}) []map[string]any {
	if focus == nil || len(focus.TriggerIntentIDs) == 0 && len(frontierIDs) == 0 {
		rows = capOverviewRows(rows, limit)
		return leanFactMaps(rows)
	}
	triggerIDs := make(map[int64]struct{}, len(focus.TriggerIntentIDs))
	for _, id := range focus.TriggerIntentIDs {
		triggerIDs[id] = struct{}{}
	}
	type candidate struct {
		row        map[string]any
		index      int
		score      int
		primary    int64
		confidence string
	}
	candidates := make([]candidate, 0, len(rows))
	for i, row := range rows {
		sources := factRowSources(row)
		score := 0
		primary := int64(0)
		for _, source := range sources {
			if _, ok := triggerIDs[source]; ok {
				score = 2
				primary = source
				break
			}
			if _, ok := frontierIDs[source]; ok && score < 1 {
				score = 1
				primary = source
			}
		}
		if primary == 0 && len(sources) > 0 {
			primary = sources[0]
		}
		confidence, _ := row["confidence"].(string)
		candidates = append(candidates, candidate{row: row, index: i, score: score, primary: primary, confidence: confidence})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		ci, cj := candidates[i].confidence, candidates[j].confidence
		if ci != cj {
			return ci == "observed"
		}
		return candidates[i].index < candidates[j].index
	})
	selected := make([]map[string]any, 0, min(limit, len(candidates)))
	perIntent := make(map[int64]int)
	for _, item := range candidates {
		if len(selected) >= limit {
			break
		}
		if item.primary > 0 && perIntent[item.primary] >= 2 {
			continue
		}
		selected = append(selected, item.row)
		if item.primary > 0 {
			perIntent[item.primary]++
		}
	}
	if len(selected) == 0 {
		selected = capOverviewRows(rows, limit)
	}
	return leanFactMaps(selected)
}

// leanFactMaps projects the selected rows. `_sources` is an internal field from
// candidate expansion and is never emitted to the planner.
func leanFactMaps(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		m := map[string]any{
			"id":      row["id"],
			"summary": leanText(row["summary"], 300),
		}
		if v, ok := row["from_intent"]; ok {
			m["from_intent"] = v
		}
		if v, ok := row["confidence"]; ok {
			m["confidence"] = v
		}
		out = append(out, m)
	}
	return out
}

func factRowSources(row map[string]any) []int64 {
	if raw, ok := row["_sources"]; ok {
		switch v := raw.(type) {
		case []int64:
			return v
		case []any:
			out := make([]int64, 0, len(v))
			for _, item := range v {
				if id, ok := structuredInt64(item); ok {
					out = append(out, id)
				}
			}
			return out
		}
	}
	if id, ok := structuredInt64(row["from_intent"]); ok {
		return []int64{id}
	}
	return nil
}

func overviewIDSet(groups ...any) map[int64]struct{} {
	out := map[int64]struct{}{}
	for _, group := range groups {
		rows, ok := overviewRows(group)
		if !ok {
			continue
		}
		for _, row := range rows {
			if id, ok := structuredInt64(row["id"]); ok {
				out[id] = struct{}{}
			}
		}
	}
	return out
}

func leanFindingRows(rows []map[string]any, limit int) []map[string]any {
	rows = capOverviewRows(rows, limit)
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		m := map[string]any{
			"id":      row["id"],
			"summary": leanText(row["summary"], 300),
		}
		if v, ok := row["from_intent"]; ok {
			m["from_intent"] = v
		}
		out = append(out, m)
	}
	return out
}

func leanHintRows(rows []map[string]any, limit int) []map[string]any {
	out := make([]map[string]any, 0, min(limit, len(rows)))
	for _, row := range rows {
		if state, _ := row["state"].(string); state != "" && state != "active" {
			continue // consumed hints are history; keep the window for active directives
		}
		out = append(out, map[string]any{
			"id":    row["id"],
			"state": row["state"],
			"text":  leanText(row["text"], 300),
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func leanDigestRows(rows []map[string]any, limit int) []map[string]any {
	rows = capOverviewRows(rows, limit)
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		m := map[string]any{
			"id":              row["id"],
			"body":            leanDigestBody(row["body"]),
			"body_is_summary": row["body_is_summary"],
			"member_count":    row["member_count"],
		}
		out = append(out, m)
	}
	return out
}

func leanDigestBody(value any) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	return firstLine(s, 300)
}

func leanCoverage(cov map[string]any) map[string]any {
	out := make(map[string]any, 6)
	for _, key := range []string{"denominator", "tested", "host_count", "pct", "status"} {
		if v, ok := cov[key]; ok {
			out[key] = v
		}
	}
	return out
}

func leanAnyList(value any, limit int) ([]any, bool) {
	var out []any
	switch v := value.(type) {
	case []any:
		out = append(out, v...)
	case []int64:
		for _, id := range v {
			out = append(out, id)
		}
	case []int:
		for _, id := range v {
			out = append(out, id)
		}
	case []string:
		for _, s := range v {
			out = append(out, s)
		}
	default:
		return nil, false
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, true
}

func leanInt64List(value any, limit int) ([]int64, int) {
	switch v := value.(type) {
	case []int64:
		total := len(v)
		if limit > 0 && len(v) > limit {
			v = v[:limit]
		}
		return append([]int64(nil), v...), total
	case []any:
		total := len(v)
		out := make([]int64, 0, len(v))
		for _, item := range v {
			if id, ok := structuredInt64(item); ok {
				out = append(out, id)
				if limit > 0 && len(out) >= limit {
					break
				}
			}
		}
		return out, total
	case []int:
		total := len(v)
		out := make([]int64, 0, len(v))
		for _, item := range v {
			out = append(out, int64(item))
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return out, total
	default:
		return nil, 0
	}
}
