# Planner 精简态势事实选择（2026-10-23）

## 范围

只影响 `OverviewLean` 的 Planner 预注入事实；完整模式、探索图、权限、审计和 UI 回放不变。

## 行为

- 以最近 20 条事实为 base；
- lean 且未用 `SERIES_PLANNER_FACT_SELECTION=off` 关闭时，额外召回：
  - 当前 `open_intents` / `running_intents` 直接 `yields` 的事实；
  - 本轮 `done` / `finding` trigger 意图直接 `yields` 的事实；
  - 与上述意图通过 `derived_from` 相连的一跳事实；
- 候选上限动态：`max(60, recent_facts*4)`，最高 120；
- 基础分：
  - trigger 直接产出 100；
  - trigger 一跳 80；
  - frontier 直接产出 40；
  - frontier 一跳 15；
  - 其他 0；
- 加成：
  - 新旧程度 0–5（按节点 ID 估算创建顺序）；
  - `confidence=observed` +3；
  - 来源意图 priority 折半 0–5；
- 同分时 `confidence=observed` 优先，再按原顺序；
- 同一意图最多 2 条；
- 最终条数完全由 `planner_overview_config` 的 `recent_facts` 控制；8、12、20 走同一套逻辑，没有写死；
- 候选查询失败保留 base 最近事实，并设置 `partial=true`；
- `SERIES_PLANNER_FACT_SELECTION=off` 可恢复旧行为。

## 代码位置

- `db/exploration.go`：`FactsYieldedByIntents` / `FactsDerivedFromIntents`，返回 `(FactID, IntentID)` 对。
- `db/tool_reads.go`：`ToolVisibleNodesByIDs`，复用现有可见性图；`ToolIntentPriorities` 读取意图优先级。
- `agent/tools.go`：`expandOverviewFacts`，候选召回、去重、hidden 过滤、优先级元数据。
- `agent/overview_lean.go`：`leanFactRows` 多因素打分、每意图上限、动态最终条数。
- `agent/planner.go`：trigger 过滤、选项传入、环境开关。

## 验证

- `gofmt -l agent db server`：无输出。
- Agent 相关测试：通过。
- `go test ./server -run '^$'`：通过。
- `go test ./db -run '^$'`：通过。
- `npx tsc --noEmit`：通过。
- Windows 构建：
  - `dist/series-windows-amd64/series-fact-weights.exe`
  - SHA-256：`5a3a6608a61eecf2c516e8882544a413a3c51d8c2014305c126d444347500a0c`
  - 版本：`fact-weights-20261023`
  - **未替换根目录 `series.exe`**：当前有 `series.exe` 正在运行；停止后用该文件替换。

## 未做

- 未做 finding/intent 相关性选择；
- 未改完整模式；
- 权重是固定常量，暂未暴露到 UI；先跑真实项目再决定是否可调；
- 当前全量 `go test ./agent` 仍有既有的 `TestRealCurlPicksUpUserAgent` 本机 curl 环境失败，与本次改动无关。