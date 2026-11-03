# 回滚 05c8e90 的规划行为（提示词层）

- 日期：2026-10-30
- 目标提交：`05c8e90`「feat(planner): 全图按需检索并强制规划前查询」（pphua，2026-10-23 17:17）
- 分支：`revert/05c8e90`

## 为什么回滚

`05c8e90` 把 Planner 的“硬底线”（目标未达成且无在跑意图时，本轮必须产出意图）替换成了“先查询已有方向再新增意图”。前者逼模型做决定，后者只要求它查——于是每轮可以无限次“再查查”后才收尾。

各任务实测（每轮首末次工具调用之间的时长与调用数）：

| 任务 | 创建时间 | 每轮工具调用 | 每轮时长 |
| --- | --- | --- | --- |
| 82 | 10-12 | 13.2 | 4.2 分钟 |
| 154 | 10-15 | 14.9 | 6.0 分钟 |
| 156 | 10-20 | 18.1 | 6.5 分钟 |
| 167 | 10-27（门槛之后） | 46.4 | 12.6 分钟 |
| 172 | 10-29（门槛之后） | 65.8 | 15.6 分钟 |

172 今天单轮已到 103~152 次调用。模型单次往返平均 29.6 秒，其中 86% 的轮次时长是模型等待，不是检索或数据库。

## 为什么没有做“整提交回滚”

`git revert 05c8e90` 实测：**8 处冲突**，且按上游旧版本整文件替换后编译失败：

```
agent\overview_lean.go:195:13: undefined: factRowOneHop
agent\overview_lean.go:216:14: undefined: primaryFactSource
agent\planner.go:453:40: too many arguments in call to wrapupSettlement
agent\tools_smartproxy.go:39:55: t.smart undefined
```

原因：该提交同时重排了提示词装配（`planner.go` −189 行、新增 `planner_bootstrap.go`）与概览/检索基建；后续 `b76c684` 又重写了 bootstrap 并删除了门槛文件。整提交回滚会把 10-23 之后建立的地基一起拔掉，并丢失分叉功能（智能代理 `t.smart`、收尾签名、`/report_human` 等）。

因此本次只回滚**行为层**，保留检索工具。

## 实际改动（3 处）

| 文件 | 改动 |
| --- | --- |
| `agent/planner.go` | 恢复心跳强制：心跳到点且 `HasActiveIntent()` 为假时，lead 明确要求“必须产出至少一个新意图（不得 0 意图）” |
| `agent/planner.go` | 恢复硬底线原文：目标未达成且 frontier 空、无 running 意图时，本轮必须产出意图，产出 0 意图=任务停摆 |
| `agent/planner_bootstrap.go` | “准备新增方向时，先主动用 context_search 查询” → “先据本轮态势与图引导判断，确需核对再按需查询，不要为凑查询遍历全图”；保留上游测试要求的“仅监督或等待结果时复用已有上下文”一句 |

## 保留未动

- 检索工具与按需读取（`context_search`/`context_get`/`graph_overview` 按需）；
- `b76c684` 的“平台不以是否查询作为 add_intent 准入”；
- `335bd2e` 的默认值与校验修复；
- 分叉功能（智能代理、收尾单参签名、`/report_human`）。

## 验证

```
gofmt -l agent/                  # 无输出
go build ./agent ./server ./cmd/series   # exit 0
go vet ./agent ./server          # exit 0
go test ./agent -count=1         # ok 2.442s
```

## 副作用与边界

- 旧行为会在“确实没有新方向”时也强制派意图，可能产出低价值方向（但不再空转一小时）。
- 若后续要兼顾“不空转”与“不凑数”，需要第二层：连续两轮派不出任何**有价值**方向时，显式标记方向枯竭（`prove_goal` 或新状态），而不是无限检索。

## 已知未解决

- 提示词迁移：数据库里已保存的 Planner 提示若 hash 匹配旧默认值会被一次性替换；本次改动同样进入迁移白名单，重启后生效。
- 任务 172 的规划会话需要新的一轮才用上新提示。
