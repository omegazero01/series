# 规划者态势窗口瘦身方案（Agent View Slimming）

- 状态：提案（仅文档，未改代码）
- 分支：`proposal/agent-view-slimming`
- 基线：`main@0f47873`
- 日期：2026-10-21
- 范围：Planner 的 Agent 视图、提示词、实验开关与观测；不改探索图表结构、权限、资产锚点、冷热图算法

## 1. 摘要

把 Planner 每轮预注入的 `graph_overview` 从当前的 **full（上限 24,000 字符）** 改成 **lean（目标 6,000 字符左右）**：

- 图仍然是唯一事实源，继续承担 fact/intent/hint/digest、资产锚点、权限过滤、血缘、审计和 UI 回放；
- Planner 默认只拿目标、frontier、最近少量 fact、active hint 和关键计数；
- 细节由 Planner 按需调用 `context_search` / `context_get`；
- 默认仍走 full，lean 由设置项开启；用 A/B 数据决定是否切换默认值；
- 不改数据库 schema，不引入新的状态机，不重写 Planner/Worker。

一句话：**保留黑板作为仪表盘和事实源，但不再把整块仪表盘塞进 Planner 的每轮上下文。**

## 2. 背景

### 2.1 当前实现

以 `main@0f47873` 为准（行号可能随提交漂移）：

| 位置 | 作用 |
|---|---|
| `agent/planner.go:289` | `renderGraphOverview()` 把图快照拼进本轮 user 输入 |
| `agent/planner.go:440` | `situational := cancelled + renderTriggers(...) + renderGraphOverview(tsx.graphOverviewData())` |
| `agent/overview_projection.go:10` | `plannerOverviewRunes = 24000` |
| `agent/tools.go:489` | `graphOverviewData()` 生成 goals/hints/frontier/facts/findings/cold digest 等 |
| `agent/planner.go:316` | 提示词描述“有界态势已附在下面”，并说明 parents/yields 血缘 |
| `agent/context_tools.go:84` | `context_search` / `context_get` 的可用类型与读取预算 |
| `agent/coldgraph.go:39` | 冷热图 `R=6`、`K=2` |

当前 `graphOverviewData()` 的窗口上限：

- goals：最多 100；
- hints：最多 50；
- open intents：最多 30；
- recent done intents：最多 12；
- recent facts：最多 20；
- findings：最多 10；
- running intents：全部；
- cold digests、related tasks：参与投影。

### 2.2 问题

这不是“没有窗口”，而是“窗口仍然太大、太全”：

1. **Token 成本高**：Planner 每轮都带 24k 字符上限的图快照，长任务会反复付费。
2. **信息污染**：大量历史 fact/intent 与当前 frontier 无关，模型容易被旧结论带偏。
3. **结构噪声**：`parents`/`yields`、`evidence`、`report`、`body` 等字段很长，但 Planner 多数轮次只需要 ID、摘要和计数。
4. **职责混淆**：人类指令（hint）、世界事实（fact）、执行结果（intent output）都放在同一张图里，Planner 容易把“指令”当“证据”。
5. **已有按需读取能力未被充分利用**：`context_search` / `context_get` 已经存在，但 Planner 默认拿到太多，反而没有动力按需查。

### 2.3 不做什么

本方案明确不做：

- 不删除 `exploration_nodes` / `exploration_edges` / `exploration_anchors`；
- 不把图改成 Markdown 或工作区文件；
- 不改资产授权、审批、审计、证据链；
- 不改 `coldgraph` 的 R/K 和折叠算法；
- 不改 Worker 的提示词和工具面；
- 不改模型、provider、并发数；
- 不引入 Oracle/Intent/Probe 新状态机；
- 不做前端大改版。

这些都属于后续独立提案，不能和本方案混在一起做归因。

## 3. 目标与非目标

### 3.1 目标

1. Planner 每轮预注入的态势字符数降到 full 的 **40% 以下**（目标 4k–8k，默认 6k）。
2. 成功率不下降（非劣于 full，允许 5% 以内波动）。
3. Planner 仍能完成：目标判定、frontier 去重、串行链规划、失败复盘、证据引用。
4. 所有省略内容都有明确的 `sections` 读取入口，且读取受当前任务权限约束。
5. 默认行为不变；lean 可通过设置项开启和回滚。
6. 全程可观测：能回答“这轮 Planner 看到了多少、少了什么、多查了什么”。

### 3.2 非目标

- 不证明“黑板无用”；本方案的前提是图继续存在。
- 不解决 Hint 语义问题（单独提案）。
- 不解决 cold digest 的摘要质量（只观测，不改算法）。
- 不改变人类在 UI 上看到的全图。

## 4. 设计原则

1. **图是事实源，投影是视图。** 不因为 Agent 看不到就删除数据。
2. **先给目录，再给正文。** Lean 视图保留 section 元数据、计数、`has_more` 和读取入口；正文按需拉取。
3. **保留因果骨架。** 即使压缩，也保留 intent 的 parents/yields 的 ID 和数量，避免 Planner 失去方向感。
4. **最近优先。** recent facts 优先取 active/running intent 产出的、时间最近的记录。
5. **权限不降级。** Lean 只过滤已经过 `authorizedNodesForStore` 的数据，不新增查询范围。
6. **默认关闭。** 先 full 为默认，lean 通过开关启用，实验通过后再改默认。
7. **可回退。** 任何异常都能切回 full，不需要数据库迁移。

## 5. 总体方案

### 5.1 双视图

```text
exploration_nodes / exploration_edges / exploration_anchors
        │
        ├── fullGraphOverview()
        │     ├── UI 任务图 / 回放
        │     ├── graph_overview 工具（人类/其他 Agent）
        │     └── 其他需要完整态势的调用方
        │
        └── leanGraphOverview()
              └── Planner 每轮预注入的 user context
                    └── 细节通过 context_search / context_get 读取
```

关键点：**不是替换 `graphOverviewData()`，而是新增一个纯过滤层**。Full 路径保持原样，避免 UI、工具、其他 Agent 回归。

### 5.2 OverviewProfile

在 `agent` 包新增：

```go
type OverviewProfile string

const (
    OverviewFull OverviewProfile = "full"
    OverviewLean OverviewProfile = "lean"
)
```

Planner 增加 resolver：

```go
overviewProfileFn func() OverviewProfile

func (p *Planner) SetOverviewProfile(fn func() OverviewProfile) {
    p.overviewProfileFn = fn
}

func (p *Planner) overviewProfile() OverviewProfile {
    if p.overviewProfileFn != nil {
        if v := p.overviewProfileFn(); v == OverviewLean {
            return OverviewLean
        }
    }
    return OverviewFull
}
```

默认 full；resolver 每轮读取，修改设置后对下一轮生效，不需要重建 Agent。

### 5.3 Lean 投影规则

新增纯函数：

```go
func leanGraphOverviewData(full map[string]any) map[string]any
```

要求：

- 不修改入参（测试要断言 source 未被 mutate）；
- 只做字段裁剪和列表截断；
- 保留 `unavailable` / `partial`；
- 保留 `sections` 的读取入口；
- 所有 `read` 指针继续经过 `scopeOverviewReads`，带上当前 `task_id`。

建议上限（可配置常量，不要散落魔法数）：

| 区块 | Lean 保留 | 上限 | 说明 |
|---|---|---|---|
| `task` | id、标题、目标、根节点 | 1 | 原样保留 |
| `goals` | id、state、text | 20 | 通常很少，全留 |
| `frontier_open` | 总数 | 1 | 必留 |
| `open_intents` | id、summary、state、parents(≤5 ID)、yields_count | 8 | 去重靠 summary |
| `running_intents` | id、summary、state、最近活动 | 6 | 让 Planner 知道谁在跑 |
| `recent_done_intents` | id、summary、state、产出 fact ID | 5 | 失败/阻塞复盘用 |
| `recent_facts` | id、summary、confidence、from_intent | 8 | 最近优先 |
| `finding_list` | id、summary | 3 | 完整列表按需查 |
| `hints` | id、text、state | 10 | 先保留现有语义，后续再按 origin 过滤 |
| `cold_digests` | id、summary、成员数 | 3 | 不展开 body/members |
| `related_tasks` | 只留计数 | 1 | 不注入正文 |
| `counts` | facts、findings、done_intents、coverage | 全部 | 防止“没展示=不存在” |
| `sections` | shown、has_more、read、unavailable | 全部 | 读取入口 |

字段裁剪：

- `summary` 截到约 300 字符；
- `text`（goal/hint）截到约 300–500 字符；
- `parents` / `yields` 只留前 5 个 ID，附 `parents_count` / `yields_count`；
- 删除 `evidence`、`report`、`body`、`raw`、`members` 等长字段；
- `related_tasks` 不保留嵌套 `recent_facts`，防止跨任务上下文膨胀。

示例：

```json
{
  "task": {"id": 156, "title": "...", "goal": "..."},
  "goals": [{"id": 1, "state": "open", "text": "..."}],
  "frontier_open": 3,
  "open_intents": [
    {
      "id": 42,
      "summary": "对 admin.example.com 的登录接口做默认口令与限速测试",
      "state": "open",
      "parents": [17, 21],
      "parents_count": 2,
      "yields_count": 4
    }
  ],
  "running_intents": [
    {"id": 43, "summary": "...", "state": "running", "last_activity": "..."}
  ],
  "recent_done_intents": [
    {"id": 39, "summary": "...", "state": "done", "fact_ids": [88, 89]}
  ],
  "recent_facts": [
    {"id": 88, "summary": "登录页无验证码，连续 5 次 401", "confidence": "observed", "from_intent": 39}
  ],
  "finding_list": [{"id": 12, "summary": "..."}],
  "hints": [{"id": 5, "text": "优先后台", "state": "active"}],
  "cold_digests": [{"id": 3, "summary": "...", "member_count": 12}],
  "counts": {"facts": 128, "findings": 7, "done_intents": 41, "coverage": {"pct": 0.42}},
  "sections": {
    "recent_facts": {
      "shown": 8,
      "has_more": true,
      "read": {"tool": "context_search", "args": {"types": ["fact"], "limit": 10}}
    },
    "finding_list": {
      "shown": 3,
      "has_more": true,
      "read": {"tool": "context_search", "args": {"types": ["finding"], "limit": 10}}
    }
  }
}
```

### 5.4 渲染与预算

`agent/overview_projection.go`：

```go
const (
    plannerOverviewRunes     = 24000 // full，保持现状
    plannerLeanOverviewRunes = 6000  // lean，目标值
)
```

`renderGraphOverview` 增加 profile 参数：

```go
func renderGraphOverview(data map[string]any, profile OverviewProfile) string
```

流程：

1. `profile == lean` 时先 `leanGraphOverviewData(data)`；
2. 再走现有 `boundedGraphOverview(view, budget, 0)`；
3. 最后加 `<graph_overview>` 包装；
4. `profile == full` 时完全走原路径。

注意：

- `boundedGraphOverview` 现有的 round-robin 机制继续保留，防止某个大 section 饿死其他 section；
- lean 的 section 上限在过滤层完成，预算层只做最后兜底；
- lean 的 `sections` 元数据也计入预算；
- 如果 lean 过滤后 `boundedGraphOverview` 返回 `context_unavailable`，按 5.6 回退。

### 5.5 提示词

当前 `plannerDefaultTmpl` 第 316 行附近假设“每个意图带 parents/yields”。Lean 下某些字段可能只有 ID/计数，因此提示词要改成**模式中立**：

```text
每个意图可能带 parents/yields；若窗口只给了 ID 或计数，说明完整血缘被省略，
需要时用 context_get 的 parents 视图读取，不要因为字段缺失就假设没有上游。
```

在 `situational` 前追加动态提示（不依赖用户的 `agent_prompts` 自定义模板）：

```text
【本轮态势模式：lean】
本轮只预注入目标、frontier、最近事实、active hints 和计数。
未展示的 facts/findings/历史意图不代表不存在。
信息不足时先用 context_search / context_get 查询，再判断目标或派意图。
不要凭概览中的摘要猜测完整证据。
```

Full 模式不追加这段，保持现有提示词行为。

如果用户已经自定义了 Planner 模板，动态提示仍然生效；不强制覆盖用户模板。默认模板的文案更新需要走现有的 prompt 版本/种子机制，不在本方案里做强制迁移。

### 5.6 回退与错误处理

| 情况 | 行为 |
|---|---|
| lean 投影后预算仍超限 | `boundedGraphOverview` 截断并保留 `sections`/`has_more` |
| `graphOverviewData` 返回 `context_unavailable` | 记录日志；本次回退 full 24k，避免 Planner 完全失明 |
| 某个 section `unavailable` | 保留 `unavailable`，不伪造空列表 |
| lean 过滤函数 panic/异常 | 捕获后回退 full；绝不让 Planner 拿到空态势 |
| 权限查询失败 | 保持现有的 `unavailable` + `partial` 语义，不做额外授权 |

回退只影响本轮，不改变设置；下一轮仍按当前 profile 尝试。

### 5.7 可观测性

在 Planner 每轮记录结构化日志：

```text
task_id, round, overview_profile, overview_chars,
sections_shown, omitted_total, fallback_reason,
planner_context_search_calls, planner_context_get_calls
```

落点建议：

- 先走现有 logger（不新增表）；
- 若需要长期分析，可从 `llm_usage` + `activity` 聚合，不急着加 schema；
- 在任务详情的 Planner activity 中可显示一行“态势: lean / 5.8k chars / 省略 37 条”，方便人工排查。

### 5.8 安全与权限

- Lean 只消费 `graphOverviewData()` 已经授权过滤后的结果；
- 不新增数据库查询，不绕过 `authorizedNodesForStore`；
- `sections.read` 必须继续使用 `scopeOverviewReads`，把 `task_id` 固定为当前任务；
- 不把 `related_tasks` 的正文放进 lean，避免跨任务信息泄漏；
- 不把 `evidence`/`report` 原文放进 lean，减少敏感内容进入模型上下文的面积。

## 6. 配置与开关

### 6.1 设置键

在 `server/manager.go` 增加：

```go
settingPlannerOverviewMode = "planner_overview_mode"
```

取值：

- `full`：默认，当前行为；
- `lean`：启用精简投影。

读取：

```go
func (m *Manager) PlannerOverviewMode() string {
    v, _, _ := m.pg.GetSetting(settingPlannerOverviewMode)
    if v == "lean" {
        return "lean"
    }
    return "full"
}
```

### 6.2 API

`server/server.go` 的 `settingsPayload()` 增加：

```go
"planner_overview_mode": s.m.PlannerOverviewMode(),
```

`putSettings` 增加：

```go
PlannerOverviewMode *string `json:"planner_overview_mode"`
```

校验只允许 `full` / `lean`；写入后不需要 `applyLLM`，因为 Planner 每轮读取 resolver。

### 6.3 前端

在“系统 → 设置 → 实验功能”卡片中增加一个下拉或开关：

- 名称：`Planner 态势窗口`；
- 选项：`完整（当前）` / `精简（实验）`；
- 说明：精简模式只影响 Planner 每轮预注入的图内容，不影响 UI、审计和权限；细节仍会按需检索；
- 默认 full；
- 切换即时生效，对下一轮 Planner 生效。

如果不希望第一版就加 UI，可先只做 API + 环境变量，实验阶段用 API 切换；UI 放到第二阶段。

### 6.4 环境变量（可选）

为隔离实验，可支持：

```text
SERIES_PLANNER_OVERVIEW=lean
```

优先级：环境变量 > 设置项 > 默认 full。仅用于测试实例，不写入生产默认。

## 7. 文件级改动清单

| 文件 | 改动 | 预计 |
|---|---|---|
| `agent/overview_projection.go` | `OverviewProfile`、lean 常量、`leanGraphOverviewData`、`renderGraphOverview` 参数 | 主要改动 |
| `agent/planner.go` | profile 字段/resolver、`situational` 拼接、动态 lean 提示 | 主要改动 |
| `agent/tools.go` | 可选：`graphOverviewDataFor(profile)`；不改原 `graphOverviewData` | 小 |
| `server/manager.go` | 设置键与 `PlannerOverviewMode()` | 小 |
| `server/server.go` | settings payload/PUT；Planner 注入 resolver | 小 |
| `server/task_llm.go` | per-task Planner 注入 resolver | 小 |
| `web/src/app/(main)/system/settings/_components/general-settings.tsx` | 实验开关（可选） | 小 |
| `agent/overview_projection_test.go` | lean 投影、预算、不可变、权限 scope 测试 | 必做 |
| `agent/context_window_metrics_test.go` | full/lean 字符数对比日志 | 建议 |
| `web` 测试 | 若加 UI，`tsc` + `biome` + `test:ui` | 条件 |

预计生产代码 300–500 行，测试 200–300 行；无 SQL 迁移。

## 8. 数据流

```text
Planner.Plan()
  ├─ tsx.graphOverviewData()          // 已经过授权过滤
  ├─ profile = p.overviewProfile()     // full / lean
  ├─ renderGraphOverview(data, profile)
  │    ├─ full: boundedGraphOverview(data, 24000)
  │    └─ lean: leanGraphOverviewData(data)
  │              └─ boundedGraphOverview(lean, 6000)
  ├─ situational = cancelled + triggers + overview
  ├─ input = lead + situational + ...
  └─ LLM
        └─ 需要细节时:
             context_search / context_get
               └─ 现有权限与 task_id scope
```

## 9. 测试计划

### 9.1 单元测试

1. `TestLeanOverviewCaps`
   - 构造 80 条 intents/facts/findings/hints；
   - 断言 lean 的条数、字段、`sections.has_more` 正确。
2. `TestLeanOverviewDoesNotMutateSource`
   - 调用前后对 full map 做 JSON 比较。
3. `TestLeanOverviewKeepsUnavailable`
   - 某个 section 查询失败时，保留 `unavailable`，不变成空数组。
4. `TestLeanOverviewBudget`
   - 构造超大字段，断言输出 ≤ 6000 字符。
5. `TestLeanOverviewScope`
   - 断言 `sections.read.args.task_id` 是当前任务，不出现其他任务 ID。
6. `TestLeanOverviewOmitsLongFields`
   - `evidence`、`report`、`body`、`members` 不出现。
7. `TestPlannerOverviewProfileDefault`
   - resolver 为 nil / 非法值时回退 full。
8. `TestPlannerPromptLeanGuidance`
   - lean 输出包含“未展示不代表不存在”“按需 context_search”。
9. `TestFullOverviewUnchanged`
   - full 路径输出与改造前一致（golden 或字符数/关键字段断言）。

### 9.2 集成测试

- 用 fake provider 捕获 Planner 的实际 user input；
- 断言 lean 模式下 `overview_chars <= 6000`；
- 断言 `goals`、`frontier_open`、`counts` 存在；
- 断言 full 模式仍为 24k 上限；
- 断言 `context_search` / `context_get` 工具仍可调用，权限 scope 正确。

### 9.3 回归命令

```bash
gofmt -l agent server
go test -count=1 ./agent ./server
go build -tags embedui ./cmd/series
```

如果改了前端：

```bash
cd web
npx tsc --noEmit
npx biome check src/app/'(main)'/system/settings/_components/general-settings.tsx
npm run test:ui
NEXT_EXPORT=1 npx next build
```

## 10. A/B 验证计划

### 10.1 分组

| 组 | Planner 态势 | 其他 |
|---|---|---|
| A | full（当前） | 不变 |
| B | lean | 不变 |
| C（后续） | lean + Worker 交接 notes | 本方案不做 |

### 10.2 样本

- 12–20 个任务；
- 覆盖简单、多阶段、长链路；
- 同一目标、同一模型/profile、同一 worker 数、同一超时；
- 每个任务 3 次，随机顺序；
- 在隔离实例/独立数据库运行，不碰用户正在跑的任务。

### 10.3 指标

| 指标 | 定义 |
|---|---|
| 成功率 | 目标达成 / 任务完成 |
| 总耗时 | 任务开始到结束 |
| 总 token | `llm_usage` 汇总 |
| Planner 输入 token | 每轮 usage |
| Planner 轮次 | activity 中 planner run 数 |
| `overview_chars` | 新增日志 |
| 检索调用 | `context_search` / `context_get` 次数 |
| 重复 intent 率 | summary 归一化 + asset_ids 比对 |
| 空转轮次 | 0 新 intent 且无 open/running |
| 冷 digest 漏事实 | planted-fact 检查 |
| 人工干预 | hint / steer / kill 次数 |

### 10.4 判定

- 成功率：B 不低于 A 的 95%；
- Planner 输入 token：B 比 A 降低 ≥30%；
- 总耗时：B 的 p50 ≤ A 的 110%；
- 重复 intent：B ≤ A 的 110%；
- 长链路：B 的 p90 不差于 A。

不满足则保持 full，不为了“架构先进”强行切换。

## 11. 上线与回滚

1. **阶段 0**：只加日志/指标，不改行为；确认 baseline。
2. **阶段 1**：lean 投影 + 设置项，默认 full；在测试实例开启。
3. **阶段 2**：跑 A/B；期间 full/lean 可随时切换。
4. **阶段 3**：数据达标后，把默认改为 lean；保留 full 作为回退。
5. **阶段 4**：观察真实任务 1–2 周；若出现重复劳动/漏事实，切回 full。

回滚：

```text
planner_overview_mode=full
```

无需数据库迁移、无需回滚代码、无需重启（resolver 每轮读；若设置缓存有延迟，最多下一轮生效）。

## 12. 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| Planner 漏掉关键 fact | 重复派意图、空转 | 计数 + `has_more` + 最近事实优先；必要时 full 回退 |
| 检索调用增加 | 延迟/token 上升 | 限制 lean 下 `context_search` 默认 15 条；观测调用次数 |
| parents/yields 被截断 | 方向判断失真 | 保留 ID 和计数；提示词要求用 `context_get` 补读 |
| 冷 digest 漏信息 | 长链路误判 | 只观测，不改 coldgraph；A/B 加 planted-fact 检查 |
| 自定义提示词不兼容 | 行为不一致 | 动态 lean 提示独立于模板；默认模板改文案但不强制覆盖 |
| `graph_overview` 工具被 Planner 调回 full | 上下文再次膨胀 | 提示词限制“仅刷新时调用”；可选按 agent 返回 lean |
| 设置误开 | 任务变慢 | 默认 full；UI 标注实验；可即时回退 |

## 13. 验收标准

- [ ] Lean 模式 Planner 预注入 ≤ 6,000 字符（p95）。
- [ ] Full 模式输出与改造前一致（无回归）。
- [ ] 成功率非劣于 full（≥95%）。
- [ ] Planner 输入 token 降低 ≥30%。
- [ ] 重复 intent 率不增加超过 10%。
- [ ] 所有省略区块都有 `sections.read` 或明确计数。
- [ ] 权限 scope 测试通过，无跨任务泄漏。
- [ ] `gofmt`、`go test ./agent ./server`、`go build -tags embedui` 通过。
- [ ] 若加 UI：`tsc`、`biome`、`test:ui`、`next build` 通过。
- [ ] CHANGELOG 增加一条“实验：Planner 态势窗口精简”。

## 14. 里程碑与工作量

| 里程碑 | 内容 | 预估 |
|---|---|---|
| M0 | 日志/指标 + baseline | 1–2 天 |
| M1 | lean 过滤 + 预算 + 提示词 + 单测 | 3–5 天 |
| M2 | 设置项/API + 可选 UI | 1–2 天 |
| M3 | A/B 跑任务与分析 | 1–2 周 |
| M4 | 默认切换/回滚预案 | 1 天 |

开发工作量约 **1–2 周**；A/B 运行另计。无数据库迁移。

## 15. 开放问题

1. Lean 默认预算定 6,000 还是 8,000？建议先用 6,000，跑 3 个任务看 Planner 的检索次数。
2. `graph_overview` 工具是否对 Planner 默认返回 lean？建议第二阶段做，避免第一版改工具语义。
3. Hint 的 `origin/authority` 是否在本方案先留字段？建议只留 `hints` 上限，语义改造放到独立提案。
4. 是否需要在 UI 显示“本轮态势字符数/省略条数”？建议先日志，稳定后再上 UI。
5. 自定义 Planner 模板的用户如何收到 lean 提示？动态提示已覆盖；默认模板文案更新可后续做。

## 16. 附录：Full vs Lean 对照

| 项目 | Full | Lean |
|---|---|---|
| 预算 | 24,000 字符 | 6,000 字符 |
| goals | 最多 100 | 最多 20 |
| open intents | 最多 30 | 8 |
| running intents | 全部 | 6 |
| recent done | 12 | 5 |
| recent facts | 20 | 8 |
| findings | 10 | 3 |
| hints | 50 | 10 |
| cold digests | 全部（受预算） | 3 |
| related tasks | 含正文 | 只留计数 |
| parents/yields | 完整 | 前 5 ID + 计数 |
| evidence/report/body | 可能进入 | 不进入 |
| 细节读取 | 可选 | 默认按需 |

## 17. 参考资料

- 微信文章《黑板之外——走向更开放的 AI 渗透架构设计》，ChainReactor；
- SERIES 现有上下文工程：`agent/overview_projection.go`、`agent/context_tools.go`、`agent/coldgraph.go`；
- 相关验证记录：`docs/verification/context-tools-20261018/measurements.txt`。