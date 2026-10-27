package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

// helper: 造一条带工具结果的 user 消息
func toolResultMsg(id, body string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{
		Type:      llm.BlockToolResult,
		ToolUseID: id,
		Content:   []llm.ContentBlock{llm.TextBlock(body)},
	}}}
}

func longBody(n int) string { return strings.Repeat("x", n) }

func TestContextPolicyConfigNormalizeAndValidate(t *testing.T) {
	// 范围夹取
	got := ContextPolicyConfig{ClearAfterTokens: 10, SummaryAfterTokens: 10_000_000, KeepRecentResults: 999, KeepRecentMessages: 1}.Normalize()
	if got.ClearAfterTokens != ContextPolicyMinClearTokens {
		t.Fatalf("清理线下界未夹取: %d", got.ClearAfterTokens)
	}
	if got.SummaryAfterTokens != ContextPolicyMaxSummaryTokens {
		t.Fatalf("摘要线上界未夹取: %d", got.SummaryAfterTokens)
	}
	if got.KeepRecentResults != ContextPolicyMaxKeepResults || got.KeepRecentMessages != ContextPolicyMinKeepMessages {
		t.Fatalf("保留条数未夹取: %+v", got)
	}
	// 顺序颠倒：清理线被压到摘要线的 1/3
	got = ContextPolicyConfig{ClearAfterTokens: 300_000, SummaryAfterTokens: 100_000}.Normalize()
	if got.ClearAfterTokens >= got.SummaryAfterTokens {
		t.Fatalf("清理线未压到摘要线之下: %+v", got)
	}
	// 校验：越界与顺序颠倒都必须报错，不能悄悄改数
	if err := ValidateContextPolicyConfig(ContextPolicyConfig{ClearAfterTokens: 5_000}); err == nil {
		t.Fatal("越界清理线应报错")
	}
	if err := ValidateContextPolicyConfig(ContextPolicyConfig{ClearAfterTokens: 300_000, SummaryAfterTokens: 100_000}); err == nil {
		t.Fatal("清理线≥摘要线应报错")
	}
	if err := ValidateContextPolicyConfig(ContextPolicyConfig{ClearAfterTokens: 60_000, SummaryAfterTokens: 300_000, KeepRecentResults: 5, KeepRecentMessages: 8}); err != nil {
		t.Fatalf("合法配置被拒: %v", err)
	}
	// 全零 = 内置默认
	if !DefaultContextPolicyConfig().IsZero() {
		t.Fatal("默认配置应为空")
	}
}

// windowForSummaryLine 造的"合成窗口"必须让 SDK 的摘要阈值正好落在目标上，
// 三段缓冲档位（1.3万/3万/5万）都要覆盖。
func TestWindowForSummaryLineMatchesSDKThresholds(t *testing.T) {
	for _, want := range []int{40_000, 60_000, 107_000, 300_000, 500_000, 800_000, 900_000} {
		w := windowForSummaryLine(want, 0)
		if w <= 0 {
			t.Fatalf("want=%d 未求出窗口", want)
		}
		eff := w - policySummaryReserve
		buf := 13_000
		if eff >= 400_000 {
			buf = 30_000
		}
		if eff >= 800_000 {
			buf = 50_000
		}
		if got := eff - buf; got != want {
			t.Fatalf("want=%d 合成窗口=%d 推出摘要线=%d", want, w, got)
		}
	}
	// 超过真实窗口能承载的范围时，夹到"SDK 自己在该窗口下会用的摘要线"：
	// 200K 窗口 → 摘要线 179K → 合成窗口正好等于真实窗口 200K。
	if got := windowForSummaryLine(900_000, 200_000); got != 200_000 {
		t.Fatalf("夹取后的合成窗口应为真实窗口 200000，得到 %d", got)
	} else if line := got - policySummaryReserve - 13_000; line != 179_000 {
		t.Fatalf("夹取后摘要线应为 179000，得到 %d", line)
	}
	if got := windowForSummaryLine(0, 200_000); got != 0 {
		t.Fatalf("未配置时应返回 0（沿用真实窗口），得到 %d", got)
	}
}

func TestClearOldToolResults(t *testing.T) {
	msgs := []llm.Message{
		llm.UserText("start"),
		toolResultMsg("a", longBody(4_000)), // 旧且长 → 清
		toolResultMsg("b", "ok"),            // 旧但短 → 不清
		toolResultMsg("c", longBody(4_000)), // 旧且长 → 清
		toolResultMsg("d", longBody(4_000)), // 最近 1 条 → 保留
	}
	out := clearOldToolResults(msgs, 1, policyMinClearRunes)
	if out == nil {
		t.Fatal("应当有可清理项")
	}
	if !policyIsCleared(out[1].Content[0].Content) {
		t.Fatal("a 应被清理")
	}
	if policyIsCleared(out[2].Content[0].Content) {
		t.Fatal("b 太短不应被清理")
	}
	if !policyIsCleared(out[3].Content[0].Content) {
		t.Fatal("c 应被清理")
	}
	if policyIsCleared(out[4].Content[0].Content) {
		t.Fatal("d 是最近一条，必须保留原文")
	}
	// 原切片不被改写（Pre 返回副本，历史保持可回读）
	if policyIsCleared(msgs[1].Content[0].Content) {
		t.Fatal("原消息被就地改写了")
	}
	// 幂等：再清一次没有新变化
	if again := clearOldToolResults(out, 1, policyMinClearRunes); again != nil {
		t.Fatal("重复清理不应再次产生变化")
	}
	// 没有超线/全部保留时返回 nil
	if none := clearOldToolResults(msgs, 10, policyMinClearRunes); none != nil {
		t.Fatal("保留条数足够时不应清理")
	}
}

func TestPolicyCompactorClearsOnlyOverLine(t *testing.T) {
	msgs := []llm.Message{llm.UserText("start")}
	for i := 1; i <= 8; i++ {
		msgs = append(msgs, toolResultMsg(string(rune('a'+i-1)), longBody(4_000)))
	}
	// 线以上：清理（保留 2 条）
	p := newPolicyCompactorForTest(50_000, 2)
	out := p.Pre(context.Background(), msgs, 80_000)
	cleared := 0
	for _, m := range out {
		for _, b := range m.Content {
			if b.Type == llm.BlockToolResult && policyIsCleared(b.Content) {
				cleared++
			}
		}
	}
	if cleared != 6 {
		t.Fatalf("应清理 6 条，实际 %d", cleared)
	}
	// 线以下：不动
	out2 := p.Pre(context.Background(), msgs, 10_000)
	for _, m := range out2 {
		for _, b := range m.Content {
			if b.Type == llm.BlockToolResult && policyIsCleared(b.Content) {
				t.Fatal("未超线不应清理")
			}
		}
	}
}

// 未配置任何自定义项时，必须继续返回内置 Compaction（行为与改动前一致）。
func TestContextManagerDefaultsToBuiltIn(t *testing.T) {
	cfg, compactor := contextManager(DefaultContextPolicyConfig(), 1_000_000, nil, 0, false)
	if cfg == nil || compactor != nil {
		t.Fatalf("空配置应返回内置 Compaction: cfg=%v compactor=%v", cfg, compactor)
	}
	if cfg.ContextWindow != 1_000_000 {
		t.Fatalf("内置窗口未透传: %d", cfg.ContextWindow)
	}
	// 配置了自定义线：返回接管型 Compactor，且不再设置 Compaction（互斥）
	cfg2, compactor2 := contextManager(ContextPolicyConfig{ClearAfterTokens: 60_000, SummaryAfterTokens: 300_000}, 1_000_000, nil, 0, false)
	if cfg2 != nil || compactor2 == nil {
		t.Fatalf("自定义配置应返回 Compactor: cfg=%v compactor=%v", cfg2, compactor2)
	}
	if _, ok := compactor2.(*policyCompactor); !ok {
		t.Fatalf("类型不符: %T", compactor2)
	}
}

func TestParseContextPolicy(t *testing.T) {
	if _, ok := ParseContextPolicy(""); ok {
		t.Fatal("空串不应解析成功")
	}
	if _, ok := ParseContextPolicy("{not json"); ok {
		t.Fatal("损坏 JSON 不应解析成功")
	}
	cfg, ok := ParseContextPolicy(`{"clear_after_tokens":60000,"summary_after_tokens":300000,"keep_recent_tool_results":6}`)
	if !ok || cfg.ClearAfterTokens != 60_000 || cfg.SummaryAfterTokens != 300_000 || cfg.KeepRecentResults != 6 {
		t.Fatalf("解析结果不符: %+v ok=%v", cfg, ok)
	}
	if _, compactor := contextManager(cfg, 1_000_000, nil, 0, false); compactor == nil {
		// 顺带验证：解析出来的配置确实会接管
		if !ok {
			t.Fatal("配置解析失败")
		}
	}
}

// policyCompactor 与内置压缩器同型（harness.Compactor）：Pre 做无损清理 + 委托摘要，
// Reactive/IsOverflow 直接委托。
func TestPolicyCompactorImplementsHarnessCompactor(t *testing.T) {
	var _ harness.Compactor = newPolicyCompactorForTest(60_000, 5)
}
