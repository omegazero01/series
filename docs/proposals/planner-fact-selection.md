# Planner 事实相关性选择实施方案

日期：2026-10-22  
状态：方案，未改代码  
> 本文是详细分析版，已被 `planner-fact-selection-v2.md` 取代；实施以 v2 为准，本文保留作背景和风险清单。  
目标：精简态势按“当前未完成方向”和“本轮唤醒事件”选择事实，而不是按输入顺序取前 8 条。

## 0. 自查修正（先看这一节）

初版方案有三处需要修正，实施时按本节为准：

1. **候选事实的 `from_intent` 可能丢失**  
   `factFrom` 是由 `EdgesWithin(visible)` 构建的；如果触发意图是较早的 `done`，不在 `recent_done_intents` 的 12 条窗口里，它就不在 `visible` 集合，候选事实的 `from_intent` 会被 `restrictNodeOrigins` 丢掉。  
   **修正**：把 focus 涉及的意图节点先加入 `visible`（仍需通过授权过滤），或者直接从候选查询结果维护 `factID → intentID` 映射；不能只靠现有 `visible`。

2. **“frontier 永远完整”这句话不准确**  
   现有 `graphOverviewData` 的 `openIntentsCap = 30`，`leanGraphOverviewData` 只是不再额外截断。超过 30 个 open intent 时，源数据本身就不完整。  
   **修正**：本方案承诺“不新增截断”，不承诺突破现有 30 上限；若要真正全量 frontier，需要单独改源查询，不能混在本次改动里。

3. **“一跳图关系”在初版里没有落地路径**  
   初版表格写了“一跳关系 +20”，但没有说明查哪些边、怎么授权、什么时候触发。没有具体查询的规则不能算方案。  
   **修正**：第一版只做“最近事实 + frontier/trigger 的直接 `yields` 事实”；一跳关系放到第二阶段，用 `EdgesTouching` 明确实现后再加。这样候选来源可解释、可测试。

另外三个实施约束：

4. **A/B 不能连续跑同一个任务**  
   同一任务跑完一轮后图已经变化，第二次不是同一初始状态。必须用克隆任务、同一数据库快照或离线回放，否则 A/B 不成立。

5. **回退数据要用未扩展的基线**  
   `renderGraphOverview` 在 lean 元数据放不下时会回退完整模式。回退时应使用未扩展的 `graphOverviewData()`，否则“完整模式基线”会被候选召回污染；至少要在结果里标明本轮使用了扩展候选。

6. **最终预算可能二次截断**  
   `leanFactRows` 选完 8 条后，`boundedGraphOverview` 仍会按总字符预算做分区轮询。选出的事实可能因为 `why` 字段、元数据或后续分区而再次被截。  
   **修正**：选择阶段预留元数据/字段开销，或在 `boundedGraphOverview` 之后记录实际 `shown`，不要假设“选中 8 条就一定展示 8 条”。

## 1. 问题定义

当前链路：

```text
graphOverviewData()
  → ToolLocalNodePageExcluding(fact, 20)
  → 最多最近 20 条事实
  → leanGraphOverviewData()
  → leanFactRows(rows, 8)
  → 取前 8 条
```

这不是“选择”，是“先截 20，再截 8”。

关键限制：

- `leanGraphOverviewData` 是纯投影函数，拿到的 `full` 已经只有最近 20 条事实；
- 一条较早但和当前方向强相关的事实，如果不在最近 20 条里，加任何相关性分数都看不到；
- `renderGraphOverview(data, profile)` 当前不接收本轮 `triggers`；
- 完整模式作为 A/B 基线，暂时不改。

因此本方案分两阶段：

1. **候选召回**：把和 frontier / trigger 相关的事实捞进候选池；
2. **相关性选择**：在候选池里打分、分组、配额、按预算输出。

只做第 2 步，只是在 20 条里换顺序，不解决旧事实丢失。

### 1.1 需求侧核对：现有搜索不全是“事实没进窗口”

对 36 份 Planner 转录做只读统计，`context_search` 共 802 次，其中 769 次来自 `exp156`。按参数拆分：

- 660 次带 `q`（关键词查询）；
- 142 次不带 `q`；
- 只有 23 次带 `intent_id`；
- 类型分布（可多选）：`fact` 533、`finding` 430、`intent` 170。

`exp156` 单独看：

- 769 次中 639 次带 `q`；
- 130 次不带 `q`；
- 不带 `q` 的 130 次里，`intent` 75、`finding` 25、`fact` 只有 19；
- 只有 20 次带 `intent_id`。

另外，`context_get` 共 406 次；按 items 统计，空 view 448、`detail` 314、`output` 161、`members` 9。大部分是读具体节点详情或 worker 输出，不是“事实摘要没进窗口”。

再看返回内容：在 `exp156` 中，类型包含 `fact` 且有结果的 `context_search` 有 395 次，返回 2,410 条 fact item；其中只有 252 条出现在调用前的最近一次态势窗口里，2,158 条在窗口外（约 89.5%）。这说明窗口确实漏掉了 Planner 想查的大量事实；但这些事实很多是由关键词 `q` 触发的，单靠 frontier/trigger 关系召回不一定能覆盖。

这也说明：**大部分搜索/读取是 Planner 主动按关键词查证据，或读取节点详情，不是“最近事实窗口少给了几条”。** 本方案主要影响“和 frontier/trigger 相关的事实是否提前进入窗口”，不能消灭关键词查询，也不能替代 finding/intent/detail 查询。

因此验收目标要改：

- 不能把“`context_search` 总数下降”当作唯一成功标准；
- 先看“不带 `q` 且只查 fact 的搜索”是否下降；
- 再看重复意图、任务得分、关键证据是否更早出现；
- 如果目标是总搜索量大幅下降，单靠事实选择不够，需要后续再做 finding/intent 的相关性选择或提示词调整。

注意：这些历史转录大多来自完整模式或更早版本，只能说明“搜索需求的形状”，不能当作 lean 模式的基线，也不能证明搜索就是事实截断造成的。

### 1.2 决策门：先确定这次改动的目标

这个改动有两类可能目标，不能混为一谈：

**目标 A：提高 Planner 的态势质量**
- 让当前方向相关的旧事实更早出现；
- 减少重复验证、重复意图；
- 预期得分提升，搜索次数不一定下降。
- 这个目标下，本方案值得做。

**目标 B：减少 `context_search` 总数**
- 现存搜索大部分是带 `q` 的关键词查询和 `context_get` 详情读取；
- 本方案只影响“事实是否提前进入精简窗口”，无法覆盖这些查询；
- 这个目标下，只做事实选择不够，应该优先做 finding/intent 的相关性选择、搜索前提示或工具使用策略调整。

如果目标是 B，却按本方案实施，最后很可能出现“代码改了、窗口变了、搜索量没降、得分也没明显变化”的结果。必须先选 A 或 B，再决定是否继续。

### 1.3 先做成本最低的实验

在写候选召回代码之前，先用刚做好的可配置数值做一组便宜实验：

```text
lean + recent_facts=8
lean + recent_facts=12
lean + recent_facts=20
```

同一个任务快照、同一模型、同一提示词，各跑一次，比较：

- 无 `q` fact 搜索；
- 带 `q` fact 搜索；
- 重复意图；
- 任务得分；
- token / 耗时；
- `sections.recent_facts.shown` 实际展示了几条（不能只看配置值）。

注意：`recent_facts=20` 在 lean 的 10k 预算下可能仍只显示 8 条；如果配置 20、实际 `shown` 还是 8，说明瓶颈是预算而不是条数，应该先调 `budget_runes` 再做相关性选择。

如果只把 8 提到 12/20 就明显改善，说明瓶颈是“窗口太小”，相关性选择的收益可能有限；  
如果 12/20 仍然找不到关键旧事实，才说明瓶颈是“选错了事实”，值得做本方案。

这一步不需要写新代码，能先排除“其实只要多给几条”的可能。

## 2. 范围

### 做

- 仅影响 `OverviewLean` 的 Planner 预注入事实；
- 新增候选事实召回；
- 新增事实相关性打分和配额；
- 每轮从 `triggers` 传入唤醒意图；
- 失败时回退当前“最近 N 条”行为；
- 用环境变量做 A/B 开关。

### 不做

- 不改探索图结构、数据库 schema、权限、审计；
- 不改完整模式的默认行为；
- 不改 `open_intents` / `running_intents` 的完整性约束；
- 不引入 LLM 打分或 embedding 检索；
- 不新增用户可见的复杂旋钮；只复用已存在的 `recent_facts` / `budget_runes`。

## 3. 数据结构

### 3.1 焦点

在 `agent` 内新增：

```go
type OverviewFocus struct {
    TriggerIntentIDs []int64
    FrontierIDs      []int64 // open + running；由 graphOverviewData 内部填充
}
```

来源：

- `TriggerIntentIDs`：`Plan()` 里 `triggers []TriggerEvent` 中 `Kind` 为 `done` / `finding` 且 `IntentID > 0` 的意图；
- `FrontierIDs`：`graphOverviewData` 已经查出的 `open_intents` + `running_intents`。

**触发类型过滤**：只把 `done`、`finding` 这类“产生了新信息”的事件当作正向 focus。`cancelled` 是用户否定一个方向，不应把该意图的旧事实提升为 trigger 相关；`goal` / `hint` 没有 `IntentID`，自然只用 frontier。否则用户取消一个方向后，旧事实反而会被拉回 Planner 窗口。

### 3.2 候选事实

候选池由三部分组成：

1. **最近事实**：当前 `ToolLocalNodePageExcluding(fact, 20)`；
2. **唤醒事件直接产出的事实**（优先）：
   - 唤醒意图的 `yields`；
   - 如果是 finding 触发，取 finding 的 `from_intent` 对应意图的产出；
3. **未完成方向直接产出的事实**：
   - `exploration_edges` 中 `intent --yields--> fact`；
   - 即 `FactsYieldedByIntents` 的批量版本。

顺序必须是“最近事实 → trigger 候选 → frontier 候选”，并在候选上限里给 trigger 预留名额。否则 frontier 的事实更多，trigger 的旧事实会在打分前就被候选上限挤掉。

去重后，候选池上限：

```text
candidate_cap = min(80, max(40, recent_facts * 4))
trigger_candidate_cap = max(10, candidate_cap / 4)
```

- 先填 `trigger_candidate_cap` 条 trigger 候选；
- 再填 frontier 候选；
- 最后用最近事实补足；
- 没有 trigger 时，trigger 名额全部让给 frontier/最近事实。

**召回边界**：本方案不是全库事实检索。只有“最近 20 条 + 与当前 frontier/trigger 有直接 `yields` 关系”的旧事实能被召回；完全无关的旧事实仍然只能靠 `context_search`。这是刻意的范围控制，不要把它宣传成“所有相关事实都能自动找到”。

`recent_facts` 是最终展示上限；候选池大小不暴露给用户。

### 3.3 存储接口

新增批量查询，避免对 30 个 open intent 逐个查：

```go
type FactYield struct {
    FactID   int64
    IntentID int64
}

func (s *ExplorationStore) FactsYieldedByIntents(
    ctx context.Context, intentIDs []int64, limit int,
) ([]FactYield, error)
```

SQL 形状：

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

不要只返回 `[]int64`。同一条事实可能被多个意图产出，单值 `from_intent` 会丢掉其他来源；用 `(FactID, IntentID)` 对保留来源集合，打分时只要任一来源命中 focus 即可，输出时选优先级最高的一个作为 `from_intent`。

约束：

- 只查当前 exploration；
- `intentIDs` 必须来自已经授权过滤的 frontier / trigger；
- 结果最终仍走与现有事实查询相同的 `workerRead` / task-node visibility 条件，或 `authorizedNodesForStore` / `visibleNodeIDs` 过滤；不要用 `NodesByIDs` 绕过原有可见性；
- 由于 `ToolNodesByIDs` 本身不带 `toolNodeVisibilitySQL`，建议新增一个只读批量接口（例如 `ToolVisibleNodesByIDs`）复用现有可见性 SQL；只在 `expandOverviewFacts` 里调用 `ToolNodesByIDs` 再补 `authorizedNodesForStore` 容易和 base 的可见性规则产生偏差；
- 候选节点需要 `confidence` 字段；`ToolNodesByIDs` 的 compact payload 有该字段，但构造 `recent_facts` 时要显式复制，不能只依赖 `compactNode` 的 id/summary；
- 冷节点/hidden 节点不进入候选；
- 查询上限取 `candidate_cap × 2`，先过滤 hidden/未授权，再截到 `candidate_cap`，避免隐藏节点占满配额；
- 对 `intentIDs` 去重；空数组直接跳过查询；
- 现有 `idx_expedges_src (src_id, rel)` 可覆盖该查询；仍要在 30 个意图、数百条事实的任务上跑一次 `EXPLAIN (ANALYZE, BUFFERS)`，确认没有退化为顺序扫描。

## 4. 召回流程

在 `graphOverviewData` 中抽出：

```go
func (t *ToolSet) graphOverviewData() map[string]any {
    // 现有逻辑，产出 base
}

func (t *ToolSet) expandOverviewFacts(
    base map[string]any, focus OverviewFocus,
) map[string]any {
    // 复制 base，查询 focus 产出的事实，合并候选
}
```

这样：

- `graph_overview` 工具和旧调用不传 focus，行为不变；
- Planner 每轮只查一次 base，再在 base 上扩展候选；不会为了候选再完整查一遍 goals/hints/frontier；
- 完整模式仍使用 base，保持基线；
- lean 回退时直接使用 base，不会污染 A/B 基线。

具体顺序：

1. 先调用 `graphOverviewData()` 得到 base；
2. 如果 `focus` 非空，调用 `expandOverviewFacts(base, focus)`；
3. 收集 trigger 意图 ID 和 frontier 意图 ID，分别去重；
4. 先查 trigger 意图的产出，预留 trigger 候选名额；
5. 再查 frontier 意图的产出；
6. 去掉已在 base 最近事实里的 ID；
7. 用 `ToolNodesByIDs` 批量取节点；
8. 过滤 hidden / 未授权；
9. 追加到 base 的 `recent_facts` 副本，总量不超过 `candidate_cap`；
10. 重新构建 `visible` / `EdgesWithin` 或直接维护 `factID → intentID`，确保新增事实带 `from_intent`；
11. 候选事实必须按和 `recent_facts` 相同的字段生成：`id`、`state`、`summary`、`from_intent`、`confidence`；不要只塞 `compactNode` 的 id/summary，否则后面无法打分；
12. `out["facts"]` 仍然使用 base 的 `facts` 总数，用于 `has_more`。

**不要把 `expandOverviewFacts` 写成第二次完整 `graphOverviewData` 查询**，否则每个规划轮会把 goals/hints/frontier/findings 全部查两遍。

失败处理：

- 批量查询失败：保留 base 最近事实，设置 `partial=true` / `fact_selection_degraded=true`；**不要**把 `recent_facts` 写进 `unavailable`，否则 `graphOverviewData` 的 defer 会删掉本来可用的 base 事实；
- `leanGraphOverviewData` 目前在白名单里复制 `partial`，但不会自动复制新键；若使用 `fact_selection_degraded`，必须把它加入复制白名单，否则 lean 视图看不到这个标记；更简单的做法是只依赖 `partial=true` 并在日志里记录原因；
- focus 为空：完全走旧逻辑；
- 候选为空：退回最近 20 条，不给空窗口；
- lean 渲染失败回退完整模式时，使用未扩展的 `graphOverviewData()` 基线；不要把带候选的 `overview` 直接当完整模式，否则 A/B 基线会被污染。

## 5. 打分与选择

### 5.1 输入

`leanGraphOverviewData` 增加一个 focus 参数（具体形式可以是 options，不必直接改公开签名）：

```go
type leanOptions struct {
    Limits OverviewLimits
    Focus  *OverviewFocus
}
```

### 5.2 关系权重

第一版用可解释规则，不调用模型：

| 关系 | 初始分数 |
|---|---:|
| 本轮 trigger 意图的 `yields` | 100 |
| 来自本轮 trigger 意图的事实 | 80 |
| 来自 frontier 意图的事实 | 40 |
| 仅最近事实 | 0 |

第一版不计算“一跳关系”。一跳关系需要额外查询 `EdgesTouching`、补齐授权和冷节点过滤，放到第二阶段；否则规则无法解释，也无法稳定测试。

同组分不足时，用以下信号打破平局：

- `confidence=observed` 高于 `inferred`；
- 时间越新越优先；
- 最后按事实 ID 稳定排序。

### 5.3 配额

对于最终上限 `recent_facts`：

- trigger 相关：约 25%；
- frontier 相关：约 50%；
- 探索/高置信度其他事实：约 25%；
- 每个意图最多 2 条；
- 如果某一类不足，剩余名额按总分补。

示例（`recent_facts=8`）：

```text
trigger:   2
frontier:  4
explore:   2
```

示例（`recent_facts=20`）：

```text
trigger:   5
frontier: 10
explore:   5
```

配额比例只是初始值，后续 A/B 调整。

### 5.3.1 确定性选择算法

不要让实现者自由发挥。按下面顺序执行：

1. 把所有候选分成四组：`trigger`、`frontier`、`explore`、`recent`；  
   同一条事实只归最高优先级组：trigger > frontier > explore > recent。
2. 用最大余数法把 `recent_facts` 分给前三组；先保证每组至少 1 条（总数允许时），再按比例分配。
3. 每组内按分数降序、时间/ID 升序排序。
4. 依次从 trigger、frontier、explore 组取，遵守：
   - 每组配额；
   - 每意图最多 2 条；
   - 已选事实跳过。
5. 如果某组不足，把剩余名额交给下一组；三组都取完后，用 `recent` 组补足。
6. 每次加入前估算序列化大小；超过 `budget_runes` 就跳过该条，继续尝试下一条更短的事实，直到没有可加入的候选。
7. 最终按事实原始时间顺序或分数顺序输出，必须固定一种，避免每轮抖动；建议按“组优先级 + 分数”输出。

`why` 的优先级同样是：

```text
trigger > frontier > explore > recent
```

一条事实既来自 trigger 又来自 frontier 时，`why=trigger`。

边界规则：

- 没有 trigger 时，把 trigger 配额按比例并回 frontier/explore，不能留下空槽；
- 多个 trigger 共享 trigger 配额，同一 trigger 最多 1–2 条，避免最后一个事件独占；
- `recent_facts` 很小时（例如 1–2 条）按“至少 1 条 frontier”优先，再分配其他组，不能因为配额取整得到空窗口。

### 5.4 预算

最终选择同时受两个约束：

```text
最多 recent_facts 条
且序列化后不超过 budget_runes
```

先按分数和配额填充，超过预算时停止；不要先取满条数再让 `boundedGraphOverview` 随机截断。

### 5.5 输出字段

被选中的事实保留：

```json
{
  "id": 123,
  "summary": "...",
  "from_intent": 45,
  "confidence": "observed",
  "why": "trigger"
}
```

`why` 只取小写枚举：

- `trigger`：本轮唤醒直接相关；
- `frontier`：当前未完成方向；
- `recent`：仅因较新入选；
- `explore`：非当前方向但高置信度。

第一版不输出 `relation`；一跳关系在第二阶段实现后再加。

`why` 的作用是让 Planner 知道“为什么这条在这里”，同时方便日志排查。若担心 token，可先只在调试字段/日志里输出，不放进 prompt；第一版建议保留，每条只增加几个字符。

同时在 `leanOverviewGuidance` 增加一句：

```text
事实的 why 表示入选原因：trigger=本轮唤醒直接相关，frontier=未完成方向，
recent=仅因较新入选，explore=非当前方向但证据较强。why 不是可信度，可信度看 confidence。
```

否则模型可能把 `why` 误读成严重级别或证据等级。

## 6. 接入点

### 6.1 `agent/planner.go`

当前：

```go
profile := p.overviewProfile()
config := p.overviewConfig()
situational := ... renderGraphOverview(tsx.graphOverviewData(), profile, config)
```

改为：

```go
profile := p.overviewProfile()
config := p.overviewConfig()
base := tsx.graphOverviewData()
overview := base
var focus OverviewFocus
if profile == OverviewLean && factSelectionEnabled() {
    focus = OverviewFocus{TriggerIntentIDs: triggerIntentIDs(triggers)}
    overview = tsx.expandOverviewFacts(base, focus)
}
situational := ... renderGraphOverview(overview, profile, OverviewRenderOptions{
    Config: config,
    Focus:  &focus,
    Base:   base,
})
```

`base` 只用于 lean 渲染失败时回退完整模式；正常路径不额外序列化它。

`triggerIntentIDs`：

```go
for _, ev := range triggers {
    if ev.IntentID <= 0 {
        continue
    }
    switch ev.Kind {
    case "done", "finding":
        ids = append(ids, ev.IntentID)
    }
}
```

`goal` / `hint` / 心跳触发没有 `IntentID`，自然退化为只用 frontier。`cancelled` 不进入正向 focus，避免把用户刚否定的方向重新拉回窗口。

### 6.2 `agent/tools.go`

- 新增 `expandOverviewFacts(base, focus)`；
- 保留现有 `graphOverviewData()` 不动，作为 base 和完整模式基线；
- 只在 focus 非空时扩展候选事实；
- 现有 `graph_overview` 工具调用不变；
- `candidate_cap` 内部计算，不进入用户配置。

### 6.3 `agent/overview_lean.go`

- `leanGraphOverviewData` 接收 focus/limits；
- `leanFactRows` 先排序再截断；
- 保留 `open_intents` / `running_intents` 不截断；
- 无 focus 时完全保持旧顺序，作为回退。
- `renderGraphOverview` 增加可选 focus/base 参数；旧的两参数调用仍按默认值工作，避免破坏现有测试。

Go 不能直接写“两个 variadic 参数”。建议用 options struct：

```go
type OverviewRenderOptions struct {
    Config OverviewConfig
    Focus  *OverviewFocus
    Base   map[string]any
}

func renderGraphOverview(
    data map[string]any,
    profile OverviewProfile,
    opts ...OverviewRenderOptions,
) string

type LeanOptions struct {
    Limits OverviewLimits
    Focus  *OverviewFocus
}

func leanGraphOverviewData(
    full map[string]any,
    opts ...LeanOptions,
) map[string]any
```

这样现有 `renderGraphOverview(data, OverviewFull)` 和 `leanGraphOverviewData(full)` 测试仍能编译；Planner 传 `OverviewRenderOptions{Config: config, Focus: &focus, Base: base}`。

迁移注意：

- 现有 `leanGraphOverviewData(full, limits)` 要改成 `leanGraphOverviewData(full, LeanOptions{Limits: limits})`；
- 现有 `renderGraphOverview(full, OverviewLean, cfg)` 要改成 `OverviewRenderOptions{Config: cfg}`；
- 不迁移这两处，编译会直接失败。

### 6.4 `agent/overview_config.go`

- 不新增用户可见字段；
- 可增加内部常量 `overviewCandidateMax = 80`、`overviewCandidateMultiplier = 4`；
- `recent_facts` 语义明确为“最终展示上限”。

### 6.5 环境变量

新增：

```text
SERIES_PLANNER_FACT_SELECTION=on|off
```

- 默认 `off`，先合并代码但保持旧行为；
- 本地验证和 TSecBench A/B 时显式设为 `on`；
- 回滚只需设 `off`，不需要回退代码；
- 后续验证通过后，可把 lean 模式默认改为 `on`。

## 7. 测试

### 7.1 单元测试

1. **旧事实被召回**  
   构造最近 20 条无关事实，第 21 条来自 frontier；focus 召回后，它应进入候选池并被选中。

2. **trigger 优先**  
   同一意图的 trigger 产出应排在普通 frontier 事实之前。

3. **frontier 投影不截断**  
   25 个 open intent、3 个 running intent 在 `leanGraphOverviewData` 的投影结果里仍全部保留；  
   注意：这不等于最终渲染一定全部出现。`boundedGraphOverview` 仍可能因 10k 预算只展示一部分，并标 `has_more`。测试要分别断言“数据层保留”和“渲染层 shown”。

4. **无 focus 回退**  
   `focus=nil` 时输出与旧 `leanFactRows` 完全一致。

5. **旧 trigger 意图的 from_intent 不丢**  
   构造一个 `done` 意图，故意让它不在 `recent_done_intents` 的 12 条窗口里；它产出的事实被召回后，必须仍然带 `from_intent`，否则本方案的核心排序会失效。

6. **无 trigger / 多 trigger / 极小上限**  
   无 trigger 时配额回收；多个 trigger 不互相挤占；`recent_facts=1` 时仍至少有一条 frontier 或 trigger 事实，不出现空窗口。

7. **取消方向不回灌**  
   `cancelled` trigger 不产生 trigger focus；该意图的旧事实只能按 recent/其他类别进入，不能因为“刚取消”反而被提升。

8. **trigger 候选不被 frontier 挤掉**  
   构造 30 个 frontier 意图、每个大量事实，再加 1 条很旧的 trigger 事实；在 `candidate_cap` 限制下，trigger 事实必须仍在候选池里。

9. **候选查询失败保留 base**  
   模拟 `FactsYieldedByIntents` 返回错误；`recent_facts` 仍保留最近 20 条，不能因为扩展失败把整个 section 标成不可用。

10. **候选去重与授权**  
    同一事实被多个意图产出时只出现一次；hidden 或未授权事实不进入候选。

11. **配额与去重**  
    同一意图最多 2 条；重复 ID 只出现一次。

12. **预算**  
    选中的事实序列化后不超过 `budget_runes`；还要断言经过 `boundedGraphOverview` 后 `sections.recent_facts.shown` 与预期一致，避免二次截断被忽略。

13. **源数据不被修改**  
    `expandOverviewFacts` 和 `leanGraphOverviewData` 都不改输入 map/slice。

### 7.2 回归测试

- `go test ./agent -run 'TestLean|TestRenderGraphOverview|TestPlannerOverviewProfile|TestOverviewConfig|TestTrimGraph'`
- `go test ./server -run '^$'`
- `npx tsc --noEmit`
- `NEXT_EXPORT=1 npx next build`

### 7.3 A/B

同一模型、同一提示词、同一初始图状态，只切换：

```text
SERIES_PLANNER_FACT_SELECTION=off
SERIES_PLANNER_FACT_SELECTION=on
```

注意：不能把同一个任务连续跑两次当作 A/B。第一次运行后图已经变化，第二次的初始状态不同。必须使用：

- 克隆任务（复制同一初始探索图）；
- 同一数据库快照的两个实例；
- 或离线回放固定快照。

样本不要只用一个任务。建议至少 3–5 个代表性任务（短任务、长链路、多方向），每个任务跑多轮或多次快照，避免单次模型随机性把结论带偏。

比较：

- `context_search` 总数，并拆成 `q` / 无 `q`、`fact` / `finding` / `intent`；
- 其中“无 `q` 且只查 fact”是本次改动的首要目标；
- 带 `q` 的关键词搜索不预期大幅下降；
- 参数完全相同的重复搜索次数；
- 重复意图数；
- 任务得分；
- 总 token、总耗时；
- 关键旧事实是否进入窗口（人工抽查）。

## 8. 验收标准

第一阶段（候选召回 + 选择）必须满足：

- 旧的相关事实能被召回，不依赖它是否在最近 20 条；
- 不新增 frontier 截断；现有 `openIntentsCap = 30` 的源数据上限保持不变；
- 这里说的是数据层保留；渲染层仍受 `budget_runes` 和分区轮询约束，可能只展示一部分并标 `has_more`；
- 无 focus / 查询失败时退回旧行为；
- 完整模式行为不变；
- 单元测试和编译全部通过。

效果验收：

- “无 `q` 且只查 fact”的搜索下降；
- 因“事实其实在图里、只是没进窗口”导致的重复搜索下降；
- 重复意图下降；
- 任务得分不下降；
- token/耗时不超过可接受范围；
- 不要求 `context_search` 总量大幅下降——关键词查询、finding/intent 查询不在本方案的作用范围内。

如果只看到“窗口更短”，但无 `q` fact 搜索和得分没变化，说明选择逻辑没有真正命中，应回退。

## 9. 实施顺序

1. 新增 `FactsYieldedByIntents` 和测试；
2. 抽出 `expandOverviewFacts`，保持旧入口不变；
3. 增加候选召回、去重、授权过滤；
4. 在 lean 投影中实现打分、配额、预算；
5. 在 Planner 中传 trigger ID，加环境开关；
6. 补单元测试和回归测试；
7. 本地用一个长任务做 A/B；
8. 通过后再考虑默认开启和 UI 展示。

## 10. 风险与回滚

| 风险 | 处理 |
|---|---|
| 批量查询慢 | 只查 focus 意图，候选上限 80；失败回退最近 20 |
| 相关性误判 | 保留探索配额和每意图上限；规则可解释 |
| 旧事实过多挤掉新事实 | 配额 + 预算 + 每意图上限 |
| trigger 噪声 | trigger 只占 25%，不占满 |
| 完整模式被污染 | 完整模式不传 focus，保持基线 |
| 评测不可复现 | 环境变量锁定开关；配置仍可固定 |
| 引入回归 | 开关默认 off；一键回滚 |

## 11. 完整设计形态（非第一版）

> 下面是设计上限，不是第一版实施范围；第一版见 §12。

```text
Planner 唤醒
  → 收集 trigger 意图 ID
  → base graphOverviewData()
      → expandOverviewFacts()（候选）
      → 去重 + 授权 + 冷节点过滤
  → leanGraphOverviewData()
      → 关系打分
      → 分组配额
      → 按预算填充
      → 输出 why/from_intent/confidence
  → Planner 输入
```

这套方案的关键不是“把 8 改成 20”，而是：

> **先让相关事实进入候选池，再决定哪几条进入窗口。**

## 12. MVP 裁剪：哪些现在不该做

当前方案是“设计完备版”，不是“第一版”。按现有数据，很多部分属于过度设计；全做会让代码量、测试量和回归风险超过可能收益。

### 12.1 必做（如果决定实施）

- 先跑 `recent_facts=8 / 12 / 20` 的便宜实验；
- 只改 lean，不改完整模式；
- base + frontier/trigger 直接 `yields` 事实；
- frontier 不新增截断；
- trigger 只认 `done` / `finding`，排除 `cancelled`；
- 简单关系分：trigger > frontier > recent；
- 去重、hidden/未授权过滤；
- 查询失败保留 base；
- 环境开关 + 默认关闭；
- 单元测试：旧事实召回、trigger 优先、失败回退、frontier 保留。

### 12.2 可以延后

- 25/50/25 的精细配额；
- `explore` 分组；
- `why` 字段和对应提示词说明；
- 多 trigger 的独立配额；
- 多来源 `FactYield` 对（先按单来源，遇到真实多来源再加）；
- 动态 `candidate_cap`（先用固定 40/60）；
- `fact_selection_degraded` 标记（先用 `partial=true` + 日志）。

这些不是错，而是第一版没有数据证明它们必要。先上线简单版，观察真实输出，再决定加不加。

### 12.3 可能整项都不需要

如果 `recent_facts=20` 的实验已经明显改善搜索/得分，说明瓶颈是窗口太小，不是“选错事实”。这种情况下，相关性选择整项都可以不做，只需要保留可配置数值。

如果目标是减少 `context_search` 总量，而不是改善态势质量，这项也不值得做；现有搜索大部分是关键词查询和 detail 读取，事实选择覆盖不了。

### 12.4 第一版建议范围

```text
lean only
+ base
+ frontier/trigger direct yields
+ trigger first, frontier second
+ score: trigger=2, frontier=1, recent=0
+ cap by recent_facts
+ per-intent max 2（唯一保留的防垄断规则）
+ feature flag off by default
```

不做：一跳关系、why、多来源、复杂配额、独立 explore 组、UI 新开关。

先把这版跑通，再根据真实 transcript 决定是否加复杂度。