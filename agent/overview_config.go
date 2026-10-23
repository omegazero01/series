package agent

import "fmt"

// Overview limits are deliberately stored separately from the projection
// profile. "full" and "lean" are presets; the values below are the editable
// knobs behind those presets. Zero means "use the preset default" so partial
// settings payloads remain backward compatible.
const (
	OverviewMinBudgetRunes = 2000
	OverviewMaxBudgetRunes = 48000
	OverviewMaxGoals       = 100
	OverviewMaxRecentFacts = 20
	OverviewMaxFindings    = 10
	OverviewMaxDoneIntents = 12
	OverviewMaxHints       = 50
	OverviewMaxColdDigests = 15
)

// OverviewLimits controls how much of one graph_overview section is handed to
// the planner. Open/running intents are intentionally not represented here:
// dropping a frontier row can make the planner create duplicate work.
type OverviewLimits struct {
	BudgetRunes       int `json:"budget_runes"`
	Goals             int `json:"goals"`
	RecentFacts       int `json:"recent_facts"`
	Findings          int `json:"findings"`
	RecentDoneIntents int `json:"recent_done_intents"`
	Hints             int `json:"hints"`
	ColdDigests       int `json:"cold_digests"`
}

// OverviewConfig is the persisted full/lean configuration. It is validated at
// the API boundary and normalized again when read, so an old or partial JSON
// value can never produce a zero-row planner window.
type OverviewConfig struct {
	Full OverviewLimits `json:"full"`
	Lean OverviewLimits `json:"lean"`
}

// OverviewFocus is the per-round input for lean fact selection. Only positive
// trigger events (done/finding) belong here; cancelled directions must not pull
// their old facts back into the window.
type OverviewFocus struct {
	TriggerIntentIDs []int64
}

// LeanOptions and OverviewRenderOptions keep the existing two-argument render
// calls source-compatible while adding config/focus/base without trying to use
// multiple variadic parameters of different types.
type LeanOptions struct {
	Limits OverviewLimits
	Focus  *OverviewFocus
}

type OverviewRenderOptions struct {
	Config OverviewConfig
	Focus  *OverviewFocus
	Base   map[string]any
}

func DefaultOverviewConfig() OverviewConfig {
	return OverviewConfig{
		Full: OverviewLimits{
			BudgetRunes:       plannerOverviewRunes,
			Goals:             OverviewMaxGoals,
			RecentFacts:       OverviewMaxRecentFacts,
			Findings:          OverviewMaxFindings,
			RecentDoneIntents: OverviewMaxDoneIntents,
			Hints:             OverviewMaxHints,
			ColdDigests:       OverviewMaxColdDigests,
		},
		Lean: OverviewLimits{
			BudgetRunes:       plannerLeanOverviewRunes,
			Goals:             20,
			RecentFacts:       8,
			Findings:          3,
			RecentDoneIntents: 5,
			Hints:             10,
			ColdDigests:       3,
		},
	}
}

func (c OverviewConfig) Normalize() OverviewConfig {
	def := DefaultOverviewConfig()
	c.Full = normalizeOverviewLimits(c.Full, def.Full)
	c.Lean = normalizeOverviewLimits(c.Lean, def.Lean)
	return c
}

// ValidateOverviewConfig rejects values that the UI or API should never
// persist. Zero is allowed and means "fall back to the preset default".
func ValidateOverviewConfig(c OverviewConfig) error {
	for _, item := range []struct {
		name   string
		limits OverviewLimits
	}{
		{"full", c.Full},
		{"lean", c.Lean},
	} {
		if item.limits.BudgetRunes != 0 &&
			(item.limits.BudgetRunes < OverviewMinBudgetRunes || item.limits.BudgetRunes > OverviewMaxBudgetRunes) {
			return fmt.Errorf("%s.budget_runes 必须在 %d–%d 之间", item.name, OverviewMinBudgetRunes, OverviewMaxBudgetRunes)
		}
		checks := []struct {
			name string
			val  int
			max  int
		}{
			{"goals", item.limits.Goals, OverviewMaxGoals},
			{"recent_facts", item.limits.RecentFacts, OverviewMaxRecentFacts},
			{"findings", item.limits.Findings, OverviewMaxFindings},
			{"recent_done_intents", item.limits.RecentDoneIntents, OverviewMaxDoneIntents},
			{"hints", item.limits.Hints, OverviewMaxHints},
			{"cold_digests", item.limits.ColdDigests, OverviewMaxColdDigests},
		}
		for _, check := range checks {
			if check.val != 0 && (check.val < 1 || check.val > check.max) {
				return fmt.Errorf("%s.%s 必须在 1–%d 之间", item.name, check.name, check.max)
			}
		}
	}
	return nil
}

func (c OverviewConfig) LimitsFor(profile OverviewProfile) OverviewLimits {
	if profile == OverviewLean {
		return c.Lean
	}
	return c.Full
}

func normalizeOverviewLimits(in, def OverviewLimits) OverviewLimits {
	in.BudgetRunes = normalizeOverviewInt(in.BudgetRunes, def.BudgetRunes, OverviewMinBudgetRunes, OverviewMaxBudgetRunes)
	in.Goals = normalizeOverviewInt(in.Goals, def.Goals, 1, OverviewMaxGoals)
	in.RecentFacts = normalizeOverviewInt(in.RecentFacts, def.RecentFacts, 1, OverviewMaxRecentFacts)
	in.Findings = normalizeOverviewInt(in.Findings, def.Findings, 1, OverviewMaxFindings)
	in.RecentDoneIntents = normalizeOverviewInt(in.RecentDoneIntents, def.RecentDoneIntents, 1, OverviewMaxDoneIntents)
	in.Hints = normalizeOverviewInt(in.Hints, def.Hints, 1, OverviewMaxHints)
	in.ColdDigests = normalizeOverviewInt(in.ColdDigests, def.ColdDigests, 1, OverviewMaxColdDigests)
	return in
}

func normalizeOverviewInt(value, def, min, max int) int {
	if value <= 0 {
		value = def
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// trimGraphOverviewData applies the editable section caps to the full
// projection. Open/running intents are not touched. The input map is copied so
// a settings read cannot mutate the snapshot owned by the graph store.
func trimGraphOverviewData(full map[string]any, limits OverviewLimits) map[string]any {
	if full == nil {
		return map[string]any{}
	}
	limits = normalizeOverviewLimits(limits, DefaultOverviewConfig().Full)
	out := make(map[string]any, len(full))
	for key, value := range full {
		out[key] = value
	}
	for key, limit := range map[string]int{
		"goals":               limits.Goals,
		"recent_facts":        limits.RecentFacts,
		"finding_list":        limits.Findings,
		"recent_done_intents": limits.RecentDoneIntents,
		"hints":               limits.Hints,
		"cold_digests":        limits.ColdDigests,
	} {
		if rows, ok := overviewRows(out[key]); ok {
			out[key] = capOverviewRows(rows, limit)
		}
	}
	return out
}
