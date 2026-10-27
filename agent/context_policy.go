package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

// ContextPolicyConfig 把上下文管理里的两条"线"从"窗口推导"改成"可配置的绝对值"。
//
// 底座 SDK(norma) 的内置压缩把两条线都绑在同一个窗口上：
//
//	工具输出清理(micro，无损可回读) = 有效窗口 × 0.6
//	历史摘要(auto，有损)           = 有效窗口 − 缓冲(按有效窗口分档 1.3万/3万/5万)
//
// 窗口按 1M 配置时两条线分别被推到约 59.5 万 / 94.2 万 token，实测没有任何会话能碰到，
// 等于这两道机制被关掉了（见 docs/verification/context-policy-20261026.md 的实测数据）。
//
// 本文件在 SDK 的接管点(agentcore.Options.Compactor)上包一层：
//
//   - 清理线（clear_after_tokens，无损）：超过这条线以后，把"保留条数"之外、且正文
//     足够长的旧工具输出换成占位符；工作集本身不改写(Pre 返回副本)，被清理的正文
//     仍可从会话记录/工具里重新读取。
//   - 摘要线（summary_after_tokens，有损）：委托内置压缩器执行，但用一个"合成窗口"
//     把它自己的摘要阈值对准配置值，从而与清理线解耦（内置公式把两者的比值锁死了）。
//
// 所有字段 0 = 用内置默认（即完全走 SDK 原有的窗口推导，行为与改动前一致）。
type ContextPolicyConfig struct {
	// ClearAfterTokens > 0 时启用自定义清理线（单位 token）。
	ClearAfterTokens int `json:"clear_after_tokens,omitempty"`
	// KeepRecentResults 是清理时保留的最近工具结果条数（0 = 内置默认 5）。
	KeepRecentResults int `json:"keep_recent_tool_results,omitempty"`
	// SummaryAfterTokens > 0 时覆盖历史摘要线（单位 token）。
	SummaryAfterTokens int `json:"summary_after_tokens,omitempty"`
	// KeepRecentMessages 是摘要时保留的最近消息条数（0 = 内置默认 8）。
	KeepRecentMessages int `json:"keep_recent_messages,omitempty"`
}

// 取值边界：清理线低于 2 万会在会话刚起步就动手，高于 60 万等于不生效；
// 摘要线低于 4 万会让长会话反复丢失原文，高于 90 万超出了常见模型窗口。
const (
	ContextPolicyMinClearTokens   = 20_000
	ContextPolicyMaxClearTokens   = 600_000
	ContextPolicyMinSummaryTokens = 40_000
	ContextPolicyMaxSummaryTokens = 900_000
	ContextPolicyMinKeepResults   = 1
	ContextPolicyMaxKeepResults   = 50
	ContextPolicyMinKeepMessages  = 2
	ContextPolicyMaxKeepMessages  = 50

	// ContextPolicyEnv 非空时锁定内置默认（隔离评测 A/B 不受 UI 配置影响），
	// 与 SERIES_PLANNER_OVERVIEW 同一套路。
	ContextPolicyEnv = "SERIES_CONTEXT_POLICY"

	// 与 SDK 内置清理使用的占位符保持一致，避免两种占位符并存。
	policyClearedPlaceholder = "[Old tool result content cleared]"
	// 小于这个长度的工具结果不清（一句确认/一个 id 不值得清）。
	policyMinClearRunes = 600
	// SDK 内置摘要预留（MaxOutputTokens 默认 8000，series 未改）。
	policySummaryReserve = 8_000
)

// DefaultContextPolicyConfig 返回"全部用内置默认"的空配置。
func DefaultContextPolicyConfig() ContextPolicyConfig { return ContextPolicyConfig{} }

// IsZero 表示没有自定义项，调用方应继续走 SDK 内置压缩路径。
func (c ContextPolicyConfig) IsZero() bool {
	return c.ClearAfterTokens <= 0 && c.KeepRecentResults <= 0 &&
		c.SummaryAfterTokens <= 0 && c.KeepRecentMessages <= 0
}

// Normalize 把数值夹到合法区间；非法关系（清理线 ≥ 摘要线）就地纠正为安全的默认比例，
// 保证"保存了什么就一定生效、不会两边打架"。
func (c ContextPolicyConfig) Normalize() ContextPolicyConfig {
	if c.ClearAfterTokens > 0 {
		c.ClearAfterTokens = clampInt(c.ClearAfterTokens, ContextPolicyMinClearTokens, ContextPolicyMaxClearTokens)
	}
	if c.SummaryAfterTokens > 0 {
		c.SummaryAfterTokens = clampInt(c.SummaryAfterTokens, ContextPolicyMinSummaryTokens, ContextPolicyMaxSummaryTokens)
	}
	if c.KeepRecentResults > 0 {
		c.KeepRecentResults = clampInt(c.KeepRecentResults, ContextPolicyMinKeepResults, ContextPolicyMaxKeepResults)
	}
	if c.KeepRecentMessages > 0 {
		c.KeepRecentMessages = clampInt(c.KeepRecentMessages, ContextPolicyMinKeepMessages, ContextPolicyMaxKeepMessages)
	}
	// 两条线都有值且顺序颠倒时，把清理线压到摘要线的 1/3（保留可清理区间）。
	if c.ClearAfterTokens > 0 && c.SummaryAfterTokens > 0 && c.ClearAfterTokens >= c.SummaryAfterTokens {
		c.ClearAfterTokens = clampInt(c.SummaryAfterTokens/3, ContextPolicyMinClearTokens, ContextPolicyMaxClearTokens)
		if c.ClearAfterTokens >= c.SummaryAfterTokens {
			c.ClearAfterTokens = ContextPolicyMinClearTokens
		}
	}
	return c
}

// ValidateContextPolicyConfig 校验用户提交的配置；Normalize 之前的原始输入必须能通过，
// 否则设置页给出明确报错而不是悄悄改数字。
func ValidateContextPolicyConfig(c ContextPolicyConfig) error {
	if c.ClearAfterTokens != 0 && (c.ClearAfterTokens < ContextPolicyMinClearTokens || c.ClearAfterTokens > ContextPolicyMaxClearTokens) {
		return fmt.Errorf("清理线必须在 %d–%d token 之间", ContextPolicyMinClearTokens, ContextPolicyMaxClearTokens)
	}
	if c.SummaryAfterTokens != 0 && (c.SummaryAfterTokens < ContextPolicyMinSummaryTokens || c.SummaryAfterTokens > ContextPolicyMaxSummaryTokens) {
		return fmt.Errorf("摘要线必须在 %d–%d token 之间", ContextPolicyMinSummaryTokens, ContextPolicyMaxSummaryTokens)
	}
	if c.KeepRecentResults != 0 && (c.KeepRecentResults < ContextPolicyMinKeepResults || c.KeepRecentResults > ContextPolicyMaxKeepResults) {
		return fmt.Errorf("保留工具结果条数必须在 %d–%d 之间", ContextPolicyMinKeepResults, ContextPolicyMaxKeepResults)
	}
	if c.KeepRecentMessages != 0 && (c.KeepRecentMessages < ContextPolicyMinKeepMessages || c.KeepRecentMessages > ContextPolicyMaxKeepMessages) {
		return fmt.Errorf("保留消息条数必须在 %d–%d 之间", ContextPolicyMinKeepMessages, ContextPolicyMaxKeepMessages)
	}
	if c.ClearAfterTokens > 0 && c.SummaryAfterTokens > 0 && c.ClearAfterTokens >= c.SummaryAfterTokens {
		return fmt.Errorf("清理线必须小于摘要线（否则刚清完又立刻触发摘要）")
	}
	return nil
}

// ParseContextPolicy 解析持久化的 JSON；空/损坏时回落到内置默认。
func ParseContextPolicy(raw string) (ContextPolicyConfig, bool) {
	if strings.TrimSpace(raw) == "" {
		return DefaultContextPolicyConfig(), false
	}
	var cfg ContextPolicyConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return DefaultContextPolicyConfig(), false
	}
	return cfg.Normalize(), true
}

// ContextPolicyEnvLocked 报告是否被环境变量锁定（评测隔离）。
func ContextPolicyEnvLocked() bool {
	return strings.TrimSpace(os.Getenv(ContextPolicyEnv)) != ""
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// contextManager 构造一次 run 的上下文管理选项，返回二者之一：
//   - 未配置任何自定义项：返回内置 Compaction（行为与改动前完全一致）；
//   - 配置了自定义项：返回接管用的 Compactor（Compaction 置空，SDK 只允许一个上下文管理器）。
func contextManager(policy ContextPolicyConfig, windowTokens int, provider llm.Provider, maxTokens int, nonStreaming bool) (*compaction.Config, harness.Compactor) {
	if policy.IsZero() {
		return compactionConfig(windowTokens), nil
	}
	innerWindow := windowTokens
	if w := windowForSummaryLine(policy.SummaryAfterTokens, windowTokens); w > 0 {
		innerWindow = w
	}
	if innerWindow <= 0 {
		innerWindow = defaultWindowK * 1000
	}
	inner := compaction.New(compaction.Config{
		ContextWindow: innerWindow,
		KeepRecent:    policy.KeepRecentMessages,
	}, compaction.ProviderSummarizer(provider, maxTokens, nonStreaming))
	return nil, &policyCompactor{
		inner:       inner,
		clearAfter:  policy.ClearAfterTokens,
		keepResults: policy.KeepRecentResults,
	}
}

// applyContextPolicy 在 opts 装配完成后应用上下文策略：
//   - resolver 为空或配置全为 0 → 什么都不做（保留内置 Compaction，行为不变）；
//   - 已被实验功能 noa 接管(opts.Compactor 非空) → 不覆盖，两个上下文管理器互斥；
//   - 否则按配置把 Compaction 换成接管型 Compactor。
func applyContextPolicy(opts *agentcore.Options, policy func() ContextPolicyConfig, windowTokens int) {
	if opts == nil || policy == nil {
		return
	}
	cfg := policy()
	if cfg.IsZero() {
		return
	}
	if opts.Compactor != nil {
		return // noa 等已接管，保持互斥
	}
	compactionCfg, compactor := contextManager(cfg, windowTokens, opts.Provider, opts.MaxTokens, opts.NonStreaming)
	opts.Compaction = compactionCfg
	opts.Compactor = compactor
}

// windowForSummaryLine 求一个"合成窗口"，让内置压缩器的摘要阈值正好落在 want 上：
//
//	摘要阈值 = (窗口 − 预留) − 缓冲(有效窗口 <40万→1.3万 / ≥40万→3万 / ≥80万→5万)
//
// want<=0 时返回 0（表示沿用真实窗口）。配置值超过真实窗口能承载的范围时，夹到
// "SDK 自己在该窗口下会用的摘要线"（真实窗口−预留−1.3 万），保证不会把摘要拖到
// 模型窗口边缘才触发。这样夹取后的合成窗口正好等于真实窗口。
func windowForSummaryLine(want, realWindow int) int {
	if want <= 0 {
		return 0
	}
	if realWindow > 0 {
		if limit := realWindow - policySummaryReserve - 13_000; limit > 0 && want > limit {
			want = limit
		}
	}
	w := want + policySummaryReserve + 13_000
	for i := 0; i < 8; i++ {
		eff := w - policySummaryReserve
		buf := 13_000
		if eff >= 400_000 {
			buf = 30_000
		}
		if eff >= 800_000 {
			buf = 50_000
		}
		got := eff - buf
		if got == want {
			return w
		}
		w += want - got
		if w < minWindowK*1000 {
			w = minWindowK * 1000
		}
	}
	return w
}

// policyCompactor 是"清理线可配置 + 摘要线可配置"的组合器：先做无损的工具输出清理，
// 再把结果交给内置压缩器处理有损的历史摘要。
type policyCompactor struct {
	inner       *compaction.Compactor
	clearAfter  int
	keepResults int
}

func (p *policyCompactor) Pre(ctx context.Context, msgs []llm.Message, lastInputTokens int) []llm.Message {
	gate := lastInputTokens
	if p.clearAfter > 0 && policyTokenCount(msgs, lastInputTokens) > p.clearAfter {
		cleared := clearOldToolResults(msgs, p.keepResults, policyMinClearRunes)
		if cleared != nil {
			msgs = cleared
			// 工作集刚被改写 → 让内置压缩器改用本地估算，避免拿旧计数误判摘要线
			// （SDK 自己的 microCompact 之后也是这个约定）。
			gate = 0
		}
	}
	return p.inner.Pre(ctx, msgs, gate)
}

func (p *policyCompactor) Reactive(ctx context.Context, msgs []llm.Message) ([]llm.Message, bool) {
	return p.inner.Reactive(ctx, msgs)
}

func (p *policyCompactor) IsOverflow(err error) bool { return p.inner.IsOverflow(err) }

// newPolicyCompactorForTest 构造一个"只做清理"的接管器：摘要委托给一个禁用摘要
// (summarizer=nil)的内置压缩器，单测只会走到清理路径。
func newPolicyCompactorForTest(clearAfter, keepResults int) *policyCompactor {
	return &policyCompactor{
		inner:       compaction.New(compaction.Config{ContextWindow: 1_000_000}, nil),
		clearAfter:  clearAfter,
		keepResults: keepResults,
	}
}

// policyTokenCount 复刻内置压缩器的计数口径：优先用上一次响应上报的总输入，
// 没有时用本地按块估算并 4/3 放大。
func policyTokenCount(msgs []llm.Message, lastInputTokens int) int {
	if lastInputTokens > 0 {
		return lastInputTokens
	}
	return compaction.EstimateTokens(msgs) * 4 / 3
}

// clearOldToolResults 把"保留条数之外、且正文足够长"的工具输出正文换成占位符，
// 返回改写后的副本；没有可清理项时返回 nil（调用方沿用原切片）。
// 语义与 SDK 的 MicroCompact 一致：非破坏性，被清正文可重新读取。
func clearOldToolResults(msgs []llm.Message, keepRecent, minRunes int) []llm.Message {
	if keepRecent <= 0 {
		keepRecent = 5
	}
	type candidate struct {
		msgIndex int
		blkIndex int
		id       string
	}
	var cands []candidate
	for i := range msgs {
		for j, b := range msgs[i].Content {
			if b.Type != llm.BlockToolResult || policyIsCleared(b.Content) {
				continue
			}
			if policyBlockRunes(b.Content) < minRunes {
				continue
			}
			cands = append(cands, candidate{msgIndex: i, blkIndex: j, id: b.ToolUseID})
		}
	}
	if len(cands) <= keepRecent {
		return nil
	}
	clear := make(map[string]bool, len(cands)-keepRecent)
	for _, c := range cands[:len(cands)-keepRecent] {
		clear[c.id] = true
	}
	changed := false
	out := append([]llm.Message(nil), msgs...)
	for i := range out {
		var nc []llm.ContentBlock
		for j, b := range out[i].Content {
			if b.Type != llm.BlockToolResult || !clear[b.ToolUseID] {
				continue
			}
			if nc == nil {
				nc = append([]llm.ContentBlock(nil), out[i].Content...)
			}
			nc[j].Content = []llm.ContentBlock{llm.TextBlock(policyClearedPlaceholder)}
			changed = true
		}
		if nc != nil {
			out[i].Content = nc
		}
	}
	if !changed {
		return nil
	}
	return out
}

func policyIsCleared(blocks []llm.ContentBlock) bool {
	return len(blocks) == 1 && blocks[0].Type == llm.BlockText && blocks[0].Text == policyClearedPlaceholder
}

func policyBlockRunes(blocks []llm.ContentBlock) int {
	n := 0
	for _, b := range blocks {
		n += len([]rune(b.Text))
	}
	return n
}
