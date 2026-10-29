# 联盟画像（feat/alliance-profile）验证记录

- 日期：2026-10-29
- 分支：`feat/alliance-profile`（基于 `feat/alliance-system@d08da1c`）
- 环境：Windows 11、Go 1.26、Node 20、Next.js 16.4.3
- 未使用独立测试数据库；本次没有运行任何依赖 PostgreSQL 的用例。

## 已实测

| 检查 | 命令 | 结果 |
|---|---|---|
| Go 编译 | `go build -mod=readonly ./allianceprofile ./db ./server ./agent ./cmd/series` | 通过（exit 0） |
| 前端构建（含类型检查） | `cd web && NEXT_EXPORT=1 npx next build` | 通过；路由中包含 `/function/alliance` |
| Linux 二进制 | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embedui -o dist/amd64/series ./cmd/series` | 通过；`ELF 64-bit LSB executable, x86-64, statically linked, stripped`，40,738,942 字节 |
| Docker 镜像构建 | `docker compose build series`（E:\AI\SCCBA） | 见下方“Docker 交付”一节 |

## Docker 交付

- 交付目录：`E:\AI\SCCBA`（由本分支 `git archive` 导出，保留上一版 README/部署文档）
- 端口：宿主 **8792** → 容器 8787（8787 已被其他实例占用）；宿主 127.0.0.1:**8793** → 容器 8788
- 项目名：`sccba-profile`；数据卷 `sccba-profile_pgdata`；宿主数据目录 `data/`
- Dockerfile 使用预编译 `dist/amd64/series`，不在镜像内编译前端
- 构建代理：宿主 Docker Desktop 全局代理为 `127.0.0.1:7897`，构建容器内不可用；
  compose 的 `build.args` 显式改为 `http://host.docker.internal:7897`
- 结果：构建/启动结果以交付目录 `Docker启动说明.md` 与本次会话最终报告为准

## 未验证（不得当成已通过）

1. **没有新增单元测试**：`allianceprofile` 的文档校验/裁剪、建议审核冲突、证据白名单均未写测试。
2. **没有跑数据库集成测试**：回填、版本冲突、任务快照、反哺 run/建议审核未在真实 PostgreSQL 上验证。
3. **没有手工点 UI**：画像编辑器、版本冲突提示、任务页签、建议审核流程均未在浏览器里操作过。
4. **没有验证真实模型阅读/反哺质量**：软提示阅读与建议生成只做了代码路径与编译验证。
5. **旧台账回填**：本机业务库（5435）未启动、未执行；只在代码层保证幂等条件。

## 与方案的偏差

- 方案写“会话首轮注入 + 按需工具”，实现改为**每次 planner/worker/goals/mainagent 运行都注入**：
  本平台 planner 每轮是全新的 `captureRun`，没有可复用会话，只注入首轮等于后续轮次没有背景。
  注入仍受 8000 字上限约束。
- 接受建议写 revision 时环境字段传空串（`db/alliance_profile.go` 的 `ReviewAllianceSuggestion`），
  历史快照的环境信息不完整；功能不受影响，后续修正。

## 复核命令

```bash
cd E:/AI/series
git log --oneline -3
go build -mod=readonly ./allianceprofile ./db ./server ./agent ./cmd/series
cd web && NEXT_EXPORT=1 npx next build
cd .. && ls -la dist/amd64/series && file dist/amd64/series
```