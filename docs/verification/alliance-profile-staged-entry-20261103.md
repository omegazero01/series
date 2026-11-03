# 联盟画像「首版 → 补充 → 反哺」实施验证

- 日期：2026-11-03
- 代码提交：`4473ee4`
- 运行镜像：`series:sccba-profile`，容器版本 `4473ee4`
- 设计文档：`docs/proposals/alliance-profile-staged-entry.md`
- 范围：A（未知字段留空/有效已填）、B（首版引导/阶段完成度/无修改不升版）、C（反哺环境修复 + 联调）、D（九号回退初版）

## 已实测

| 检查 | 命令/操作 | 结果 |
|---|---|---|
| 统计 helper 单测 | `node -r ./scripts/test-register.cjs --test src/lib/alliance-profile-stats.test.ts` | 3 个用例全部通过 |
| 前端类型检查 | `cd web && npx tsc --noEmit` | 通过 |
| 前端构建 | `NEXT_EXPORT=1 npx next build` | 通过 |
| Go 编译 | `go build ./allianceprofile ./db ./server ./agent ./cmd/series` | 通过 |
| 新 UI 字符串嵌入 | 容器内 `grep -a` | “有效已填”“保存首版”“继续补充”均命中 |
| 无修改保存不升版 | 对九号 profile 用相同 document/environment PUT，expected_version=3 | 返回 version=3；revision 仍为 3 条 |
| 服务健康 | `GET /api/health` | 200，version `4473ee4` |
| 容器启动 | `docker compose up -d --force-recreate series` | 成功，Postgres healthy |

## A：有效已填口径

新增 `web/src/lib/alliance-profile-stats.ts`：

- `supplied` / `confirmed` 且有内容 → 计入已填；
- `not_applicable` 且有原因 → 计入已填；
- `unknown` 一律不计入已填；有“待确认：”文字时单独计为“待确认说明”；
- 阶段和全画像统计、`firstIncompletePhase()` 都由这个 helper 计算。

界面改动：

- 编辑器顶部显示“有效已填 X/Y · 待确认说明 Z · 未填 N”；
- 阶段标签显示 `已填/总数`；
- 字段输入框提示“未知请留空；需要说明时以「待确认：」开头”；
- `unknown` 带文字时显示“待确认说明，不计入已填”；
- `not_applicable` 无原因时显示“不适用需要写原因，当前不计入已填”。

## B：首版引导与版本行为

- 新建台账后仍自动打开画像编辑器，默认停在阶段 1。
- 编辑器顶部提示“先保存一版，再慢慢补”。
- 保存按钮：`version == 1` 显示“保存首版（v2）”，之后显示“保存修改（vN）”；有未保存修改时显示“有未保存修改”。
- 没有修改时保存按钮禁用，后端也做了幂等判断：相同 document + environment 直接返回当前版本，不新增 revision。
- 列表行新增“继续补充”，打开编辑器时自动跳到第一个未完成阶段。

实测无修改保存：

```text
before version=3
PUT same document/environment, expected_version=3
after version=3
history versions = [3, 2, 1]
```

## C：反哺链路

已修复已知缺陷：

- `db/alliance_profile.go` 的 `ReviewAllianceSuggestion` 现在读取系统 `environment` 并写入 revision，不再写空串。

**未完成真实联调。** 当前 `/api/llm` 返回：

```json
{"configured": false, "key_set": false, "provider": "anthropic", "model": "", "base_url": ""}
```

本机也没有可用的 Ollama（`127.0.0.1:11434` 无响应）。因此：

- 没有真实模型生成建议；
- 没有任务产生 confirmed 证据；
- C1–C10 用例未执行；
- 不能宣称反哺链路已端到端通过。

需要用户提供可用模型（地址/密钥/模型名）后，按设计文档第 6 章执行。

## D：九号画像回退

操作：`PUT /api/alliance/systems/2/profile`，`expected_version=2`，生成 v3。

结果：

- 版本：v3；历史 v1/v2/v3 都在；
- 状态统计：17 supplied、17 unknown（其中 1 条为“待确认：”说明）、16 个空值；
- 有效已填 17，待确认说明 1，未填 16；
- 台账扁平列已恢复为短值：
  - `owner_unit` = 九号公司（Segway-Ninebot）
  - `framework` = AEM + Vue + jQuery/Granite；版本待确认
  - `business` = 产品展示、购买跳转、支持/召回；首轮目标待确认
  - `security_note` = AEM Dispatcher + CDN 缓存；数据库/缓存/队列未暴露
  - `crypto` = HTTPS/TLS；表单/接口加密与签名未确认
- S1-05 保留 `待确认：未取得测试授权与联系人，默认只读公开信息，不主动测试`。

## 未验证（不得当成已通过）

1. 没有在浏览器手工点过新 UI；只验证了类型检查、构建、字符串嵌入和接口行为。
2. 没有跑真实模型的反哺链路；C 项目前阻塞在模型配置。
3. `not_applicable` 无原因时后端仍允许保存，只在前端提示；是否符合最终验收需确认。
4. 没有跑 `go test ./...`；本次只做了编译和前端统计单测。
5. `Continue supplement` 的“跳到第一个未完成阶段”只做了代码路径，未在浏览器点击验证。

## 复核命令

```bash
cd E:/AI/series
git log --oneline -3
go build -mod=readonly ./allianceprofile ./db ./server ./agent ./cmd/series
cd web && npx tsc --noEmit
node -r ./scripts/test-register.cjs --test src/lib/alliance-profile-stats.test.ts

cd E:/AI/SCCBA
docker compose ps
curl -s http://127.0.0.1:8792/api/health
docker compose logs --tail=20 series
```