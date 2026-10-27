# 上下文压缩阈值可自定义（清理线 / 摘要线）— 验证记录

日期：2026-10-26
分支：`feat/context-policy`（worktree `E:/AI/series/.runtime/context-policy`，基线 `main@052a646`）
状态：**代码 + 单测 + 类型检查 + lint 对齐已完成；前端构建、合并、exe 替换记录见文末（构建段）**

## 1. 要解决的问题（实测依据）

底座 SDK（norma）把上下文压缩的两条线都绑在模型窗口上（`compaction/compaction.go`）：

- 工具输出清理（micro，**无损可回读**）= 有效窗口 × 60%
- 历史摘要（auto，**有损**）= 有效窗口 − 缓冲（有效窗口 <40 万→1.3 万 / ≥40 万→3 万 / ≥80 万→5 万）

本机三个 profile 的 `context_window_k` 都是 **1000**（1M），于是两条线被推到：

| 机制 | 阈值 | 本机实测是否触发 |
|---|---:|---|
| 工具输出清理 | 约 **59.5 万** token | 从未触发 |
| 历史摘要 | 约 **94.2 万** token | 从未触发 |

实测会话峰值（669 个 worker 会话）：中位 7.6 万、p90 14.3 万，最大 43 万 —— 距两条线都很远，**两道机制等于关闭**。
另一个已知样本 `exp163-worker-i11534`（49 轮）从 1.45 万单调涨到 26.2 万，全程零清理。

## 2. 实现方式（不改 SDK、不 fork）

用 SDK 自己留的接管点 `agentcore.Options.Compactor`（`agentcore/agentcore.go:67`），新增 `agent/context_policy.go`：

- `ContextPolicyConfig`：`clear_after_tokens`、`keep_recent_tool_results`、`summary_after_tokens`、`keep_recent_messages`，**全部 0 = 沿用底座默认**；
- 配置了自定义项时返回 `policyCompactor`：`Pre` 先做无损清理（把"保留条数"之外的旧工具输出正文换成占位符，**改写的是副本**，原历史不动、正文可回读），再把结果交给内置压缩器做摘要；
- 摘要线用"合成窗口"对准：由内置公式反解出一个窗口，使其 auto 阈值正好等于配置值（`windowForSummaryLine`），从而把两条线解耦；配置值超过真实窗口承载能力时夹到"底座自己在该窗口下会用的摘要线"；
- 未配置（或环境变量锁定）时**继续走内置 `Compaction`**，行为与改动前完全一致；`opts.Compactor` 已被 noa 占用时不覆盖（两个上下文管理器互斥）；
- 四类 Agent（worker / planner / mainagent / chat）各自 `SetContextPolicy(resolver)`，**每 run 读一次**，改配置不需要重建 LLM 链；8 个装配点（`server/server.go` 5 处、`server/task_llm.go` 3 处）已接线；
- 设置键 `settings.context_policy_config`（JSON，零值清键），`SERIES_CONTEXT_POLICY` 非空时强制用底座默认（与 `SERIES_PLANNER_OVERVIEW` 同套路，保证评测 A/B 不被 UI 配置污染）。

校验：清理线 2 万–60 万、摘要线 4 万–90 万、保留工具结果 1–50 条、保留消息 2–50 条，且**清理线必须小于摘要线**（否则刚清完立刻触发有损摘要）。

前端：设置页新增卡片（`general-settings.tsx`），三档预设 + 四个数值输入 + 保存 / 恢复底座默认；文案写明"清理=无损可回读、摘要=有损不可逆""保存后对之后启动的 run 生效"。

## 3. 已执行的验证（真实输出）

| 项目 | 命令 | 结果 |
|---|---|---|
| 编译 | `go build ./agent ./server ./cmd/series` | 通过（无输出） |
| 新增单测 | `SERIES_PG_DSN=<不可达> go test ./agent -run 'TestContextPolicy\|TestWindowForSummaryLine\|TestClearOldToolResults\|TestPolicyCompactor\|TestParseContextPolicy' -count=1 -v` | **6/6 PASS** |
| 前端类型 | `./node_modules/.bin/tsc --noEmit` | 通过（exit 0，无输出） |
| lint 对齐 | `biome check`（新文件 vs `main` 原文件，同一上下文） | 诊断数**完全一致**：useSortedClasses 28 / noUnnecessaryConditions 2 / 总计 42（零新增） |

单测覆盖的判据：范围夹取与顺序校验、合成窗口与 SDK 阈值逐档对齐（含 200K 窗口夹取到 179K 的边界）、"只清旧且长"、"原消息不被就地改写"、清理幂等、未超线不清、空配置走内置路径、JSON 解析。

### 3.1 关于那 2 条 `noUnnecessaryConditions` 警告（结论：**与本改动无关**）

把 `main` 上**未改动**的原文件复制到同一目录、用同一套 biome 上下文检查，得到**同样 2 条**（在原文件里位于 201/202 行，本改动把行号挤到 267/268）。它们指向既有代码 `s.traffic_skip_types ?? ""`、`s.traffic_retention_days ?? 0` —— 后端 `settingsPayload` 这两个键始终返回非空值（`strings.Join(...)` / `TrafficRetentionDays()`），TS 类型也声明为非可选，所以 `??` 是死代码；**运行时无影响**，仓库也**没有 CI**（无 `.github/`）。

注意对照方法：按**绝对路径**检查项目根之外的文件会丢掉类型解析，依赖类型信息的规则不执行（会出现假的 `noUndeclaredDependencies`），必须把文件放进同一目录再比。

本改动引入的 1 条 `useSortedClasses`（info 级）已改为 biome 规范顺序 `font-normal text-muted-foreground text-xs`（类名顺序调整，渲染样式不变）。实测该规则只认"font-* 开头"的组合：`text-xs text-muted-foreground`、`text-muted-foreground text-xs`、`text-xs font-normal ...` 都会被报，且**不提供自动修复**。

## 4. 未验证项（不要当成已验证）

1. **没有跑过真实任务的 A/B**：本次只到"编译 + 单测 + 类型 + lint"，没有克隆任务对比开启前后的 token / 缓存命中 / 轮数。要验证收益需按 `docs/verification/` 里其它 token 记录的同一口径做克隆任务对照。
2. **UI 未在浏览器里点过**：卡片渲染、保存回填、恢复默认只做了类型检查与静态核对。
3. **设置持久化链路未端到端跑**：`putSettings` → `settings` 表 → 下一 run 的 resolver 读取没有对着运行中的服务验证（本轮没有启动服务；替换 exe 后需要一次新任务才能观察）。
4. **与 noa 的互斥只在代码层保证**（`opts.Compactor != nil` 直接返回），没有实际开启 noa 同时验证。
5. `go test ./server` 未执行（需要独立测试库；全量会失败的用例与改动无关）。

## 5. 构建与替换记录（合并后填写）

- 合并提交：（待填，`git merge --no-ff feat/context-policy`）
- 前端构建：`NEXT_EXPORT=1 npx next build`（待填结果）
- exe 构建：`SERIES_SKIP_FRONTEND=1 SERIES_SKIP_NPM_CI=1 ./build.sh --target windows/amd64`（待填）
- 旧 exe 备份：`series.exe.bak-<YYYYMMDD-HHMMSS>`（待填）
- 新 exe SHA-256：（待填）