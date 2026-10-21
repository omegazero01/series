# Planner 态势窗口精简：实现与验证记录

- 日期：2026-10-22
- 分支：`proposal/agent-view-slimming`
- 基线：`main@0f47873`
- 范围：Planner 的 `graph_overview` 投影；探索图、权限、审计、UI 回放不变

## 实现

- 新增 `OverviewProfile`（`full` / `lean`）与 `Planner.SetOverviewProfile`。
- 新增 `leanGraphOverviewData`：只保留目标、frontier、最近事实、漏洞摘要、active hints、冷摘要和读取入口。
- `renderGraphOverview` 按 profile 选择 24k full 或 10k lean 预算；lean 预算不足时回退 full。
- `graph_overview` 工具和 UI 仍使用完整投影。
- 新增设置 `planner_overview_mode=full|lean`，默认 `full`；设置页实验功能可切换。
- 提示词补充 lean 模式说明：未展示不代表不存在，细节用 `context_search` / `context_get`。

## 数值校准

合成场景（8 条 open intent、5 个 goal、8 条 fact、6 条 finding、15 条 hint、5 条 cold digest）：

| 预算 | 实际容纳的 open intent |
|---|---|
| 6,000 字符 | 2 / 8（frontier 被饿死，不可接受） |
| 10,000 字符 | 8 / 8 |
| 12,000+ 字符 | 8 / 8（不再增加） |

因此首版 lean 预算定为 **10,000 字符**，优先保证 frontier 完整；6k/8k 需要后续投影元数据瘦身再尝试。

## 验证

通过：

```text
gofmt -w agent server
gofmt -l agent server        # 无输出

go test -count=1 ./agent \
  -run 'TestLean|TestRenderGraphOverview|TestPlannerOverviewProfile|TestOverviewProjection'
# ok github.com/neouks/series/agent

go test -count=1 ./server -run '^$'
# ok github.com/neouks/series/server [no tests to run]

go build -mod=readonly -tags embedui -trimpath \
  -ldflags "-s -w -buildid= -X main.version=agent-view-test" \
  -o .runtime/series-agent-view-test.exe ./cmd/series
# BUILD_EXIT=0

cd web
npx tsc --noEmit
# TSC_EXIT=0

NEXT_EXPORT=1 npx next build
# NEXT_EXIT=0（28 个静态页面）
```

说明：

- 完整 `go test ./agent` 中有一个与本次改动无关的环境依赖测试 `TestRealCurlPicksUpUserAgent` 失败（本机 curl exit 23），另有测试主动打印 panic 日志；本次相关测试均通过。
- 未启动、停止或替换正在运行的 `series.exe`；测试二进制输出到 `.runtime/` 和 `dist/`。