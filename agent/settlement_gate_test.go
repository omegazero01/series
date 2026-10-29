package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// 收尾门闸（settleGuard）的单测：SDK 收尾时不再隐藏工具，改由 hook 拒绝执行，
// 这样请求里的工具清单从头到尾不变，前缀缓存不会被破坏。

func gateFor(prompt string, guard ...string) *dispatchHooks {
	h := &dispatchHooks{}
	h.watchSettlement(&harness.Settlement{Prompt: prompt}, guard...)
	return h
}

func userTurns(texts ...string) []llm.Message {
	msgs := make([]llm.Message, 0, len(texts))
	for _, t := range texts {
		msgs = append(msgs, llm.UserText(t))
	}
	return msgs
}

func blockedBash(h *dispatchHooks) (bool, string) {
	blocked, reason, _ := h.PreToolUse(context.Background(), "Bash", []byte(`{"command":"whoami"}`))
	return blocked, reason
}

func TestSettlementGateBlocksBashOnlyAfterPrompt(t *testing.T) {
	const prompt = "预算已用完，请立即把结论写回并总结。"
	h := gateFor(prompt, "Bash")

	h.observeSettlement(userTurns("开始执行意图"))
	if blocked, _ := blockedBash(h); blocked {
		t.Fatal("收尾提示词出现前不应拦截 Bash")
	}

	h.observeSettlement(userTurns("开始执行意图", prompt))
	blocked, reason := blockedBash(h)
	if !blocked {
		t.Fatal("收尾提示词出现后应拦截 Bash")
	}
	if !strings.Contains(reason, "收尾相位") {
		t.Fatalf("拒绝理由应说明收尾相位，实际：%q", reason)
	}

	// 落库类工具必须放行，否则收尾就没意义了。
	if b, _, _ := h.PreToolUse(context.Background(), "record_fact", []byte(`{}`)); b {
		t.Fatal("收尾相位不应拦截落库工具 record_fact")
	}
}

func TestSettlementGateStaysLatched(t *testing.T) {
	const prompt = "收尾提示词：先落库再总结。"
	h := gateFor(prompt, "Bash")
	h.observeSettlement(userTurns(prompt))
	// 后续请求的最后一条是工具结果，不再包含提示词；门闸必须保持关闭态。
	h.observeSettlement(userTurns("命令输出：ok"))
	if blocked, _ := blockedBash(h); !blocked {
		t.Fatal("门闸一旦触发，本 run 内应持续生效")
	}
}

func TestSettlementGateMatchesPromptByReason(t *testing.T) {
	const perRun = "本轮预算用完，请收尾。"
	const taskTimeout = "整个任务已到超时上限，请收尾并落库。"
	h := &dispatchHooks{}
	h.watchSettlement(&harness.Settlement{
		Prompt: perRun,
		PromptByReason: map[harness.TerminalReason]string{
			harness.ReasonTimeout:  taskTimeout,
			harness.ReasonMaxTurns: perRun,
		},
	}, "Bash")

	h.observeSettlement(userTurns(taskTimeout))
	if blocked, _ := blockedBash(h); !blocked {
		t.Fatal("任务超时收尾词也应触发门闸")
	}
}

func TestSettlementGateToleratesPaddedPrompt(t *testing.T) {
	const prompt = "你的本轮步数已经用尽，现在的唯一任务是：把所有已确认的结论写回数据库，" +
		"然后用一句纯文本总结你在本意图上的关键发现。"
	h := gateFor(prompt, "Bash")
	// SDK 若在注入文本前后补了空白/说明，前缀匹配仍应命中。
	h.observeSettlement(userTurns(prompt + "\n\n(本消息由系统自动注入)"))
	if blocked, _ := blockedBash(h); !blocked {
		t.Fatal("提示词被追加内容后仍应识别为收尾相位")
	}
}

func TestSettlementGateIgnoresLookalikeText(t *testing.T) {
	const prompt = "收尾：请落库并总结。"
	h := gateFor(prompt, "Bash")
	h.observeSettlement(userTurns("收尾：请落库并总结，但先跑一条命令看看。"))
	if blocked, _ := blockedBash(h); blocked {
		t.Fatal("只是包含提示词片段的消息不应误判为收尾相位")
	}
}

func TestSettlementGateIgnoresNonUserTail(t *testing.T) {
	const prompt = "预算耗尽，请收尾。"
	h := gateFor(prompt, "Bash")
	h.observeSettlement([]llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock(prompt)}}})
	if blocked, _ := blockedBash(h); blocked {
		t.Fatal("只有 user 轮的收尾注入才应触发门闸")
	}
}

func TestSettlementGateNoopWithoutGuard(t *testing.T) {
	const prompt = "预算耗尽，请收尾。"
	h := gateFor(prompt) // 没有 guard：planner/主控等角色走这条路
	h.observeSettlement(userTurns(prompt))
	if blocked, _ := blockedBash(h); blocked {
		t.Fatal("未配置 settleGuard 的角色不应拦截任何工具")
	}
}

func TestSettlementGateNoopWithoutSettlement(t *testing.T) {
	h := &dispatchHooks{}
	h.watchSettlement(nil, "Bash")
	h.observeSettlement(userTurns("任意消息"))
	if blocked, _ := blockedBash(h); blocked {
		t.Fatal("没有 settlement 配置的 run 不应拦截任何工具")
	}
}

// 走真实 SDK 循环：收尾相位里工具清单必须与前面完全一致（否则前缀缓存照样失效），
// 而 Bash 调用必须被拒绝、且真的没执行。
func TestSettlementGateKeepsToolSurfaceStable(t *testing.T) {
	calls, turns, denied := 0, 0, false
	tool := actool.Build(actool.Spec{Name: "Bash", Schema: obj(nil), Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
		calls++
		return actool.Text("executed"), nil
	}})
	const prompt = "收尾相位：本轮预算已用完，请立即把结论写回数据库，然后输出一句总结。"
	complete := func(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		turns++
		visible := false
		for _, s := range req.Tools {
			visible = visible || s.Name == "Bash"
		}
		if !visible {
			t.Errorf("第 %d 轮：Bash 从工具清单里消失了 —— 前缀缓存会被打断", turns)
		}
		if turns <= 2 {
			return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: fmt.Sprint(turns), Name: "Bash", Input: json.RawMessage(`{"command":"whoami"}`)}}}, "tool_use", llm.Usage{}, nil
		}
		for _, m := range req.Messages {
			for _, b := range m.Content {
				if b.Type == llm.BlockToolResult && strings.Contains(blocksText(b.Content), "收尾相位") {
					denied = true
				}
			}
		}
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("settled")}}, "end_turn", llm.Usage{}, nil
	}
	opts := agentcore.Options{Tools: []actool.CoreTool{tool}, NonStreaming: true, PermissionMode: permission.ModeBypass, DisableBackgroundTasks: true,
		MaxTurns: 1, Settlement: &harness.Settlement{Prompt: prompt, MaxTurns: 3}, Deps: reviewModelDeps(complete)}
	if _, reason, err := captureRun(t.Context(), opts, "go", nil, "Bash"); err != nil || reason != harness.ReasonMaxTurns {
		t.Fatalf("reason=%s err=%v", reason, err)
	}
	if !denied {
		t.Fatal("收尾相位的 Bash 调用没有被拒绝")
	}
	if calls != 1 {
		t.Fatalf("Bash 实际执行 %d 次，收尾期应被拦住（只允许收尾前那一次）", calls)
	}
}
