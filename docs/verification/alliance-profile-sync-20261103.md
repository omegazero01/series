# 联盟画像 · 同步 main 验证记录

- 日期：2026-11-03
- 分支：`feat/alliance-profile-sync`（从 `feat/alliance-profile@a71f612` 起，合并 `main@b304e7c`）
- 合并提交：`5ec06d6`
- 合并基点：`f726be7`

## 同步了什么

`main` 比联盟画像分支多 17 个提交（`f726be7..b304e7c`），本次全部带入：

- **分步复现证据**：`findings.reproduction` 新列、`report/reproduction.go`、发现详情复现面板。
- **增量图摘要与上下文上限**：`perf(graph)`，约束 agent 上下文规模。
- **Planner 行为修复**：恢复直接意图下发、去掉规划前强制检索；回滚 `05c8e90` 的提示词行为。
- **检索契约修正**：显式空查询条件不被默认值覆盖、`asset_search.untested` 去掉隐藏默认、枚举报错列出允许值。
- **品牌与夹具收尾**：`0d689b2`。

联盟侧原有能力全部保留：画像文档/版本、任务快照、软提示阅读、反哺建议、台账统计、入口资产登记。

## 冲突与解决

实际冲突只有 2 个文件，均为“双方各自追加”：

| 文件 | 冲突原因 | 解决 |
|---|---|---|
| `CHANGELOG.md` | 双方都在 `## Unreleased` 下新增章节 | 保留「联盟画像」与「Planner/检索」两段 |
| `db/schema.sql` | 联盟加 8 张表/列，main 加 `findings.reproduction` | 两边都保留，顺序为联盟块 → main 的 reproduction |

`agent/planner.go`、`agent/worker.go`、`agent/mainagent.go`、`web/src/lib/types.ts` 为自动合并，无冲突。
合并后确认四处画像注入仍在：

- `agent/goals.go:106`
- `agent/mainagent.go:170`
- `agent/planner.go:455`
- `agent/worker.go:533`
- `server/goals.go:48` 的 `ensureAllianceProfileRead`
- `server/server.go` 的 `/api/alliance/*` 路由（台账 + 画像）

## 已实测

| 检查 | 命令 | 结果 |
|---|---|---|
| Go 编译 | `go build -mod=readonly ./allianceprofile ./db ./server ./agent ./cmd/series` | 通过（exit 0） |
| 前端类型检查 | `cd web && npx tsc --noEmit` | 通过（exit 0） |
| 前端构建 | `NEXT_EXPORT=1 npx next build` | 通过（exit 0） |
| Linux 二进制 | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embedui ...` | 通过；ELF 64-bit static stripped，40,996,990 字节 |
| Schema 全量应用 | 临时库 `series_sync_test` 执行 `db/schema.sql` | 通过（exit 0）；8 张联盟表 + `findings.reproduction` 均存在；临时库已删除 |
| Docker 重建 | `docker compose build series` + `--force-recreate series` | 通过；容器版本 `5ec06d6` |
| 启动健康 | `GET /api/health` | 200，`{"ok":true,"service":"series","version":"5ec06d6"}` |
| 启动自动迁移 | 现有库启动后查列 | `findings.reproduction` 存在，联盟表 8 张 |

## 未验证（不得当成已通过）

1. **没有跑真实任务**：Planner/Worker 的新行为、画像注入、软提示阅读、反哺闭环均未在真实模型下联调。
2. **没有手工点 UI**：分步复现面板、画像编辑器、任务页签、建议审核未在浏览器操作过。
3. **没有跑仓库测试套件**：`go test ./...`、前端单测未执行；main 自带的 planner/复现测试也未跑。
4. **没有验证历史数据回填**：现有库是空业务库，旧数据迁移路径未实测。

## 交付同步

- `E:\AI\SCCBA` 按 `a71f612..5ec06d6` 的 203 个文件差异同步：201 个新增/修改、2 个删除（`agent/planner_search_gate.go`、其测试）。
- 保留 SCCBA 自有文件：`README-SCCBA.md`、`部署说明.md`、`Docker启动说明.md`、`docker-compose.yml`、`Dockerfile*`、`.env`、`jwt.key`、`data/`、`skills/`。
- 二进制重新构建为 `dist/amd64/series`，镜像 `series:sccba-profile` 重建并启动。

## 复核命令

```bash
cd E:/AI/series
git log --oneline -3
git status -sb
go build -mod=readonly ./allianceprofile ./db ./server ./agent ./cmd/series

cd E:/AI/SCCBA
docker compose ps
curl -s http://127.0.0.1:8792/api/health
docker compose logs --tail=20 series
```