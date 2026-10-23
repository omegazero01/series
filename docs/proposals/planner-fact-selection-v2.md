# Planner 事实选择方案 v2

日期：2026-10-22  
状态：MVP 已实现；8/12/20 对比实验仍待跑  
目标：用最小成本判断“精简态势是否需要按相关性挑事实”；确认需要后，只实现一个最小版本。

> 实现记录见 `docs/verification/planner-fact-selection-20261023.md`。本方案保留决策门和实验步骤；当前代码已按 MVP 落地。

---

## 0. 先做决策，不要先写代码

现有数据表明：`context_search` 大部分是 Planner 主动按关键词查证据，不是“最近事实少给了几条”。

因此先做一个不需要改代码的实验：

```text
lean + recent_facts=8
lean + recent_facts=12
lean + recent_facts=20
```

同一任务快照、同一模型、同一提示词，各跑一次，比较：

- `sections.recent_facts.shown` 实际展示条数；
- 无 `q` 且只查 `fact` 的搜索；
- 带 `q` 的 fact 搜索；
- 重复意图；
- 任务得分；
- token / 耗时。

### 决策规则

- 配置 20 但实际 `shown` 仍是 8：瓶颈是预算，不是条数；先调 `budget_runes`，不要做相关性选择。
- 8 → 12/20 后搜索或得分明显改善：瓶颈是窗口太小；停止本方案，只保留可配置数值。
- 12/20 仍找不到关键旧事实：瓶颈是“选错事实”；进入下面的 MVP。

**本次按“直接改造”的要求先落地了 MVP；8/12/20 实验仍是上线前必跑回归。若实验显示 20 条就足够，直接关闭 `SERIES_PLANNER_FACT_SELECTION` 或回退本改动，不要继续加复杂度。**

---

## 1. 问题

当前链路：

```text
graphOverviewData()
  → 最近 20 条事实
  → leanGraphOverviewData()
  → 取前 8 条
```

这不是选择，是两次截断。

真正的问题是：

- 最近 20 条之外的旧事实，投影层看不到；
- `renderGraphOverview` 不接收本轮 trigger；
- 相关性排序无法改变候选集，只能在 20 条里换顺序。

但范围要控制：不是所有旧事实都值得召回，只召回和当前 frontier / trigger 有直接 `yields` 关系的事实。

---

## 2. 目标与非目标

### 目标

- 让和当前未完成方向、本轮唤醒事件直接相关的事实进入窗口；
- 减少“事实其实在图里，只是没进窗口”导致的重复判断和重复意图；
- 保持完整模式作为 A/B 基线。

### 非目标

- 不追求 `context_search` 总量下降；
- 不做全库语义检索；
- 不解决 finding / intent / detail 的搜索需求；
- 不做一跳关系；
- 不改数据库 schema、权限、审计；
- 不新增 UI 开关；
- 不重写完整模式。

---

## 3. MVP 范围

只做这些：

1. 仅 lean 模式；
2. base 最近事实 + frontier/trigger 的直接 `yields` 事实；
3. trigger 只认 `done` / `finding`，排除 `cancelled`；
4. 简单排序：trigger > frontier > recent；
5. 每意图最多 2 条，防止一个方向占满；
6. 最终条数由 `recent_facts` 控制；
7. 环境开关默认关闭；
8. 失败保留 base 最近事实。

明确不做：

- 25/50/25 精细配额；
- `explore` 分组；
- `why` 字段；
- 多 trigger 独立配额；
- 一跳关系；
- 动态候选公式；
- 完整模式改动；
- 新 UI。

---

## 4. 数据流

```text
Planner 唤醒
  → 收集 done/finding trigger 意图 ID
  → base = graphOverviewData()            // 原有 20 条最近事实
  → 如果 lean + env on:
        expandOverviewFacts(base, focus)  // 加 frontier/trigger 候选
  → leanGraphOverviewData(overview, opts)
        → 简单排序 + 每意图上限 + 最终 cap
  → boundedGraphOverview()
  → Planner 输入
```

完整模式不调用 `expandOverviewFacts`。

---

## 5. 类型与接口

```go
type OverviewFocus struct {
    TriggerIntentIDs []int64
}

type FactYield struct {
    FactID   int64
    IntentID int64
}

func (s *ExplorationStore) FactsYieldedByIntents(
    ctx context.Context,
    intentIDs []int64,
    limit int,
) ([]FactYield, error)

func (t *ToolSet) expandOverviewFacts(
    base map[string]any,
    focus OverviewFocus,
) map[string]any

type LeanOptions struct {
    Limits OverviewLimits
    Focus  *OverviewFocus
}

type OverviewRenderOptions struct {
    Config OverviewConfig
    Focus  *OverviewFocus
    Base   map[string]any
}
```

`renderGraphOverview` / `leanGraphOverviewData` 用 options struct，避免两个 variadic 参数导致编译失败。

---

## 6. 候选召回

### 6.1 候选上限

固定下限，动态上限：

```text
candidate_cap = clamp(max(60, recent_facts * 4), 60, 120)
trigger_candidate_cap = max(10, candidate_cap / 4)
```

- 没有 trigger 时，trigger 名额全部让给 frontier；
- 候选池总上限 `candidate_cap`；
- 最终展示仍由 `recent_facts` 决定。

`screenshot` 中的 8/12/20 都不写死在排序逻辑里：8 和 12 走 60 候选，20 走 80 候选，最终展示条数都取配置值。

### 6.2 查询顺序

先 trigger，再 frontier：

1. 收集 trigger 意图 ID（`done` / `finding`，去重）；
2. 从 base 的 `open_intents` / `running_intents` 收集 frontier 意图 ID；
3. 先查 trigger 产出，最多 `trigger_candidate_cap` 条；
4. 再查 frontier 产出，补到 `candidate_cap`；
5. 去掉已在 base 最近事实里的 fact ID；
6. 过滤 hidden / 未授权；
7. 追加到 base 的 `recent_facts` 副本；
8. 维护 `factSources: map[factID][]intentID`，保留多来源；输出时选最高优先级来源作为 `from_intent`。

### 6.3 批量 SQL

```sql
SELECT DISTINCT n.id, e.src_id
FROM exploration_edges e
JOIN exploration_nodes n
  ON n.id = e.dst_id AND n.exploration_id = e.exploration_id
JOIN exploration_nodes i
  ON i.id = e.src_id AND i.exploration_id = e.exploration_id
WHERE e.exploration_id = $1
  AND e.src_id = ANY($2::bigint[])
  AND e.rel = 'yields'
  AND i.kind = 'intent'
  AND n.kind = 'fact'
ORDER BY n.id DESC, e.src_id
LIMIT $3
```

要点：

- 返回 `(FactID, IntentID)` 对，保留多来源；
- 现有 `idx_expedges_src (src_id, rel)` 可支撑；
- 只查当前 exploration；
- 结果必须复用现有 `workerRead` / task-node 可见性，不能绕过。

### 6.4 可见性

不要直接用 `ToolNodesByIDs` 扩展候选；它不带 `toolNodeVisibilitySQL`。

建议新增一个批量只读接口：

```go
func (s *ExplorationStore) ToolVisibleNodesByIDs(
    ctx context.Context, ids []int64,
) ([]*Node, error)
```

内部复用现有事实查询的可见性条件；再过滤 cold/hidden。

### 6.5 失败处理

- 查询失败：返回 base 副本；
- 设置 `partial=true`，日志记录原因；
- 不把 `recent_facts` 写进 `unavailable`；
- 不新增 `fact_selection_degraded` 字段，避免 lean 白名单再漏复制。

---

## 7. 排序与选择

### 7.1 分数

只保留三个层级：

| 条件 | 分数 |
|---|---:|
| 任一来源意图是 trigger | 2 |
| 任一来源意图是 frontier | 1 |
| 其他（仅最近事实） | 0 |

同分时：

1. `confidence=observed` 优先于 `inferred`；
2. 较新的优先；
3. 按 fact ID 稳定排序。

### 7.2 选择

1. 按分数降序排序；
2. 同一个意图最多 2 条；
3. 取到 `recent_facts` 条为止；
4. 估算序列化大小，超过 `budget_runes` 就跳过该条；
5. 如果一条都没选出来，回退 base 最近事实的前 N 条；
6. 不输出 `why`，选择原因写日志。

`leanFactRows` 需要接收 `factSources`：

- base 事实默认 `from_intent` 就是唯一来源；
- 候选事实使用 `factSources[factID]` 判断 trigger/frontier；
- 输出 `from_intent` 时选优先级最高的来源。

---

## 8. 接入点

### `agent/planner.go`

```go
base := tsx.graphOverviewData()
overview := base
var focus OverviewFocus
if profile == OverviewLean && factSelectionEnabled() {
    focus.TriggerIntentIDs = positiveTriggerIntentIDs(triggers)
    overview = tsx.expandOverviewFacts(base, focus)
}
situational := ... renderGraphOverview(overview, profile, OverviewRenderOptions{
    Config: config,
    Focus:  &focus,
    Base:   base,
})
```

- `positiveTriggerIntentIDs` 只收 `done` / `finding`；
- `cancelled`、`goal`、`hint`、`finding_deleted` 不进入正向 focus；
- 完整模式不调用 `expandOverviewFacts`。

### `agent/tools.go`

- 新增 `expandOverviewFacts`；
- `graphOverviewData()` 保持不变；
- `graph_overview` 工具行为不变；
- 候选事实字段必须是 `id/state/summary/from_intent/confidence`。

### `agent/overview_lean.go`

- `leanGraphOverviewData` 接收 options；
- `leanFactRows` 先排序再截断；
- 无 focus 时完全保持旧行为；
- 不新增 `why`。

### 环境开关

```text
SERIES_PLANNER_FACT_SELECTION=on|off
```

默认 `off`；A/B 时显式打开。

---

## 9. 测试

### 单元测试

1. 旧事实召回：第 21 条来自 frontier，focus 后进入候选；
2. trigger 优先：trigger 事实排在 frontier 前；
3. `cancelled` 不提升：取消意图的事实不能获得 trigger 分；
4. 旧 trigger 的 `from_intent` 不丢；
5. 多来源事实：任一来源命中即可，输出主来源；
6. 候选查询失败：base 最近事实仍保留；
7. hidden / 未授权事实不进入候选；
8. 同意图最多 2 条；
9. 最终 `shown` 与 `recent_facts`、`budget_runes` 一致；
10. 无 focus 时输出与旧版本一致；
11. 输入 map/slice 不被修改。

### 回归

```text
go test ./agent -run 'TestLean|TestRenderGraphOverview|TestPlannerOverviewProfile|TestOverviewConfig|TestTrimGraph'
go test ./server -run '^$'
cd web && npx tsc --noEmit
```

---

## 10. A/B 与验收

### 测试对象

- 至少 3–5 个代表性任务；
- 同一初始图快照、同一模型、同一提示词；
- 只切换 `SERIES_PLANNER_FACT_SELECTION`。

不能把同一个任务连续跑两次当 A/B；第一次运行会改变图状态。

### 指标

- `sections.recent_facts.shown`；
- 无 `q` 且只查 fact 的搜索；
- 带 `q` 的 fact 搜索（不预期大幅下降）；
- 重复意图；
- 任务得分；
- token / 耗时；
- 关键旧事实是否进入窗口（人工抽查）。

### 通过条件

- 相关旧事实能被召回；
- 无 `q` fact 搜索下降，或重复意图下降；
- 任务得分不下降；
- token / 耗时没有明显恶化。

### 回退条件

- 只看到窗口变化，搜索和得分都没变化；
- token 明显上升但得分无改善；
- 关键事实被挤掉，导致决策变差。

---

## 11. 实施顺序

1. 跑 `recent_facts=8/12/20` 实验；
2. 实验支持“选错事实”假设后，再加批量查询；
3. 加 `expandOverviewFacts`；
4. 改 lean 排序与每意图上限；
5. Planner 接线 + 环境开关；
6. 单元测试、回归；
7. 3–5 个任务 A/B；
8. 有效再考虑默认开启。

---

## 12. 已知限制

- 只召回直接 `yields` 关系，不是全库检索；
- `openIntentsCap=30` 的源上限仍在；
- 不做一跳关系；
- 不做 finding/intent 选择；
- 不解决关键词搜索和 detail 读取；
- 权重没有真实数据校准，必须先 A/B。

---

## 13. 一句话

**先证明“窗口太小”不是唯一原因，再证明“选错事实”确实存在；两者都成立，才值得写这套代码。**