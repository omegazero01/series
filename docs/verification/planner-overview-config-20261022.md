# Planner 态势窗口数值配置（2026-10-22）

## 范围

把原来写死在 `leanGraphOverviewData` 和完整投影里的条数/字符预算改为系统设置，同时保留 `full` / `lean` 两个预设。探索图、权限、审计、UI 回放不变；只影响 Planner 每轮预注入的 `graph_overview` 投影。

## 配置

设置键：`planner_overview_config`

```json
{
  "full": {
    "budget_runes": 24000,
    "goals": 100,
    "recent_facts": 20,
    "findings": 10,
    "recent_done_intents": 12,
    "hints": 50,
    "cold_digests": 15
  },
  "lean": {
    "budget_runes": 10000,
    "goals": 20,
    "recent_facts": 8,
    "findings": 3,
    "recent_done_intents": 5,
    "hints": 10,
    "cold_digests": 3
  }
}
```

- 零值表示使用预设默认值，兼容旧配置和部分 PUT。
- `open_intents` / `running_intents` 不提供可调上限，避免截断未完成方向导致重复派意图。
- 条数上限受现有数据源约束：事实最多 20、发现 10、完成意图 12、提示 50、冷摘要 15、目标 100。
- `SERIES_PLANNER_OVERVIEW` 非空时强制使用预设默认值，供 TSecBench 等固定 A/B 变量场景使用。

## 后端行为

- `Manager.PlannerOverviewConfig()`：读取 JSON；损坏/缺失时回退默认；环境变量锁定时回退默认。
- `Manager.SetPlannerOverviewConfig()`：服务端校验后保存规范化 JSON。
- `Planner.SetOverviewConfig()`：每轮规划读取一次，下一次唤醒生效。
- 完整模式使用 `trimGraphOverviewData()` 按配置裁剪各分区；精简模式使用 `leanGraphOverviewData()` 同时裁剪字段和条数。
- 精简投影连元数据都装不下时，仍回退完整模式；回退也使用完整模式的配置预算。

## 前端

设置页「实验功能 → Planner 态势窗口」：

- 模式下拉：完整（默认）/ 精简（实验）。
- 「高级参数」展开当前模式的 7 个数值输入框。
- 每项显示范围与默认值。
- 「保存数值」提交两套完整配置；下一次规划生效。
- 「恢复当前模式默认」/「恢复全部默认」只填入表单，仍需点保存。

## 验证

- `gofmt -l agent server`：无输出。
- `go test -count=1 ./agent -run 'TestLean|TestRenderGraphOverview|TestPlannerOverviewProfile|TestOverviewConfig|TestTrimGraph'`：通过。
- `go test -count=1 ./server -run '^$'`：编译通过。
- `cd web && npx tsc --noEmit`：通过。
- `NEXT_EXPORT=1 npx next build`：28 个页面生成成功。
- Windows 二进制：`dist/series-windows-amd64/series.exe`，并已复制到仓库根目录 `series.exe`（备份 `series.exe.bak-20261022-2326`）
  - 版本：`200fc3e-dirty`
  - SHA-256：`b4d0fd787e59122af6a72aa73addb331da223836169ac5c84a6a410159f7f051`
  - `series.exe -h`：正常输出参数。

## 已知限制

- 当前项目全量 `go test ./agent` 仍有一个与本改动无关的既有失败：`TestRealCurlPicksUpUserAgent`（本机 curl 环境 exit 23）。
- 配置值超过底层查询上限不会凭空产生更多数据；UI 已限制，服务端也会校验。
- 当前只有平台级配置，不支持按任务单独设置。