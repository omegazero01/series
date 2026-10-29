# 联盟画像（feat/alliance-profile）验证记录

- 日期：2026-10-29
- 分支：`feat/alliance-profile`（基于 `feat/alliance-system@d08da1c`；UI 流程修复 `4c89e3a`）
- 环境：Windows 11、Go 1.26、Node 20、Next.js 16.4.3
- 未使用独立测试数据库；本次没有运行任何依赖 PostgreSQL 的用例。

## 已实测

| 检查 | 命令 | 结果 |
|---|---|---|
| Go 编译 | `go build -mod=readonly ./allianceprofile ./db ./server ./agent ./cmd/series` | 通过（exit 0） |
| 前端构建（含类型检查） | `cd web && NEXT_EXPORT=1 npx next build` | 通过；路由中包含 `/function/alliance` |
| Linux 二进制 | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embedui -o dist/amd64/series ./cmd/series` | 通过；`ELF 64-bit LSB executable, x86-64, statically linked, stripped`，40,738,942 字节 |
| Docker 镜像构建 | `docker compose build series`（E:\AI\SCCBA） | 见下方“Docker 交付”一节 |
| UI 流程修复 | 新建画像保存后自动打开三阶段编辑器（`4c89e3a`） | 类型检查 + Next 构建通过；字符串已确认嵌入新二进制，未手工点击 |

## Docker 交付

- 交付目录：`E:\AI\SCCBA`（由本分支 `git archive` 导出，保留上一版 README/部署文档）
- 端口：宿主 **8792** → 容器 8787（8787 已被其他实例占用）；宿主 127.0.0.1:**8793** → 容器 8788
- 项目名：`sccba-profile`；数据卷 `sccba-profile_pgdata`；宿主数据目录 `data/`
- 镜像：`series:sccba-profile`，基于 `python:3.12-slim-bookworm` 的最小运行镜像
  - 首次尝试按上游 Dockerfile 安装 apt/Node/Playwright：构建容器内 Docker Desktop 全局代理
    `127.0.0.1:7897` 不可达；改 `host.docker.internal:7897` 时通时断，apt 多次 502，npm 阶段失败。
  - 最终交付改用零外网依赖的 Dockerfile（不执行 apt/npm，只 COPY 静态二进制与 start.sh）；
    完整工具链 Dockerfile 保留为 `Dockerfile.full`，网络正常时可替换后重建。
- 实测结果（2026-10-29 16:24）：
  - `docker compose ps`：`sccba-profile-postgres-1` Up (healthy)，`sccba-profile-series-1` Up，端口 8792/8793 已发布
  - `GET /api/health` → 200
  - `GET /` 与 `GET /setup` → 200，页面标题 `SERIES — 自主渗透测试控制台`
  - `GET /api/alliance/systems`（未登录）→ 401，鉴权中间件生效
  - 启动日志：`SERIES 977909c backend listening on :8787 (data=/app/data, workers=3)`；
    数据库来源 `SERIES_PG_DSN`；`no LLM provider configured`（预期，待 UI 配置）
  - 数据库已自动建 8 张联盟表（含 `alliance_profile_*`、`alliance_task_*`），
    `alliance_systems` 的 `environment/document/version` 三列存在
  - 登录签名密钥 `jwt.key` 已挂载持久化；`--force-recreate series` 重建后 `GET /api/health` 仍为 200
- 运行镜像不含 nmap/curl/Playwright 等工具（最小镜像的取舍）；平台自身 python3/bash 可用。
  需要完整工具链时用 `Dockerfile.full`。

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