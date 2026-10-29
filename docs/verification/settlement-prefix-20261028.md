# 收尾相位不再隐藏 Bash：改用 hook 拒执行（2026-10-28）

## 背景

worker 的 run 撞到步数/时长预算时，SDK 会进入收尾相位（settlement）：注入一段收尾提示词，
并按 `Settlement.DisabledTools` 把 Bash 从**本次请求的工具清单**里删掉。工具清单排在请求
最前面（系统提示之后、全部历史之前），删一项等于改前缀 —— 模型服务端的缓存前缀校验从该
位置起全部失效，整段对话按原价重算。

任务 167 实测（`activity` 表 `kind='usage'` 累计值做差，按「收尾轮未缓存 > 2 万 且收尾后
Bash 调用归零」判定）：

| 指标 | 数值 |
| --- | ---: |
| worker 会话段 | 40 |
| 触发收尾前缀重算 | 14 |
| 收尾轮未缓存合计 | 610,116 token |
| 扣除本来应付部分后的白花 | 592,537 token |
| 占该任务总成本 | 5.6% |
| 占全部未缓存输入 | 16% |

任务 82（20.7 亿输入 token）同口径：94 段会话 / 白花 1,502 万 token / 折合空闲档 13.5 元（6.5%）。

Bash 定义本身只有 815 字节（约 200 token）—— 代价不在“删掉的内容”，而在“前缀被改写”。

## 改动

- `agent/worker.go`：`wrapupSettlement("worker")`、`wrapupSettlementForTask("worker", clamped)`
  —— 帮助函数不再接受禁用工具列表（编译期就不会再有人把 Bash 交给 `DisabledTools`）；
  `newSession(opts, "Bash")` 把守护名单交给 hook。
- `agent/wrapup.go`：新增 `settlementPrompts()`，把 `Prompt` 与 `PromptByReason` 的所有非空文本
  抽成去重列表（收尾提示词可由后台覆盖，所以按运行时的实际文本取值）。
- `agent/sdk_session.go`：
  - `dispatchHooks` 新增 `settlePrompts / settleGuard / settleNames / settling`；
  - `watchSettlement()` 装配门闸（无 settlement 或无守护名单时为空操作，planner/主控不受影响）；
  - `observeModelCalls` 改为 `observeRequest`：既记工具清单，也检查请求的最后一条 user 消息
    是否就是我们注入的收尾提示词 —— SDK 是 `append(messages, UserText(prompt))` 原样追加，
    所以精确匹配即可，不需要改提示词、不需要加标记；
  - `PreToolUse` 在收尾相位拒绝守护名单里的工具，返回可执行的拒绝话术（改走落库工具）。
- `agent/settlement_gate_test.go`：9 条测试 —— 8 条单测（触发前放行 / 触发后拒绝 / 状态保持 /
  PromptByReason / 被追加内容的前缀匹配 / 相似文本不误判 / 非 user 结尾不触发 / 未配置守护时为空操作）
  + 1 条走真实 SDK 循环的端到端回归：断言**每一轮请求的工具清单完全一致**（Bash 在收尾轮仍然可见）、
  收尾轮的 Bash 调用被拒绝并返回拒绝话术、且工具真的没有被执行（执行计数保持 1）。
- `agent/capture.go`：`captureRun` 增加可变参数 `settleGuard`，供端到端回归注入守护名单。

收尾提示词、收尾轮数预算、任务超时收尾词全部不变；“别再探测”的护栏从“藏工具”改成“拒执行”，
语义等价（模型看不到 → 模型看得到但调不动）。

## 验证

```text
gofmt -l agent                                        无输出
go vet ./agent                                        通过
go test ./agent -run TestSettlementGate -v            8/8 PASS
go test ./agent -count=1                              ok  2.817s
go build -o /dev/null ./cmd/series                    BUILD_OK
```

运行时自证：收尾相位识别成功会打一条

```text
[settlement] 收尾相位已识别：拒绝执行 [Bash]（工具清单保持不变，保住前缀缓存）
```

## 待做（上线后 A/B）

跑一个 20~40 轮的小任务两次（旧/新构建），对比“收尾轮未缓存 token”：
旧构建 5~15 万，新构建应与上一轮持平（1~3 千）。若日志里出现收尾拒绝、且该轮未缓存未回落，
说明检测命中但前缀仍在变，需要复核是否有其它按阶段改请求的地方。

## 回滚

单个 commit，`git revert` 即可回到“隐藏 Bash”的旧行为（会把 5~6% 的成本重新加回来）。