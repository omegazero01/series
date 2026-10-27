# 联盟系统（城商行联盟定制）方案

2026-10-27 起草 · 分支 `feat/alliance-system`（长期分支，**不合并 main**）

## 背景与目标

城商行联盟（多家成员银行）需要一套"作战情报台账"：人工把联盟内各业务系统登记成条目（系统名称、URL、安全描述——框架、业务、加解密、平台形态等），**新建任务时直接选这些条目当目标**，让渗透过程带着已知信息测试，并按联盟维度统计资产与覆盖情况。

四件事：

1. 联盟资产台账：成员单位 → 系统 → 入口，安全描述 + 标签；
2. 新建任务时选择联盟系统作为目标（复用现有"资产 → 任务"链路）；
3. 描述随任务进入渗透流程（分级注入，见 §3）；
4. 联盟资产统计（分布、覆盖、关联任务/漏洞）。

## 一、数据模型（纯新增表，`CREATE TABLE IF NOT EXISTS`）

| 表 | 字段（要点） | 说明 |
| --- | --- | --- |
| `alliance_members` | id, name, short, note | 成员单位（银行）；与现有 `companies` **弱关联**，不混用 |
| `alliance_systems` | id, member_id, name, url, platform, framework, crypto, business, security_note, tags[], status, created_by, created_at, updated_at | 一个"系统"= 一条联盟条目；platform ∈ {web, 小程序, app, 混合} |
| `alliance_endpoints` | id, system_id, url, host, port, scheme, note, asset_id | 系统 1—N 个入口；`asset_id` 关联标准资产库（可空，选择任务时回填） |
| `alliance_task_links` | task_id, system_id, created_at | 任务 ↔ 系统，供"被测覆盖率 / 漏洞数"统计 |

为什么独立建表而不是塞进 `assets.extra`：

- 联盟条目是**人工知识**（含加密方式、业务背景、对接说明），标准资产库是**扫描事实**——生命周期、更新方式、可见性都不同；
- 新表对上游同步零冲突；把情报写进 `extra`，上游一旦改语义就会打乱（`extra` 已被现有工具按人格化字段使用）。

## 二、UI 与接口

- 新页面 `/function/alliance`：台账 tab（成员/平台/标签/关键词筛选，CRUD，"从标准资产库导入"）+ 统计 tab。
- 导航加一项「联盟系统」，**由设置开关控制**（默认关闭），不影响非联盟环境。
- API（新文件 `server/alliance.go`）：`/api/alliance/members`、`/systems`、`/endpoints`、`/stats`。
- 新建任务页（`web/src/components/tasks/task-creation-form.tsx`）加选择器「联盟系统」，选中后：
  1. 把系统入口解析成标准资产（存在即复用，不存在按现有插入路径登记）；
  2. 走现有 `AssetIDs` 链路 → 自动进 `task_scope`、自动批准（沿用"任务创建时直接选择"规则；审批模板仍可控制范围）；
  3. 生成目标简报写入任务描述（见 §3）。

## 三、描述怎么"带着进任务"（分两级，控制 token）

- **第一级 · 目标简报（一次性、有界）**：选中系统压缩成一段文本写进任务 `description`，格式 `系统名 | 入口URL | 一句话（框架/平台/业务）`；预算：每个系统 ≤3 行、最多 10 个系统、总长 ≤1500 字，超出只列名字。注意：任务目标会进入 planner 提示并**每轮计价**（`agent/planner.go` 的「原始任务目标」），必须短。
- **第二级 · 按需详情**：完整安全描述随任务起点保存（origin fact 附加字段或首个 digest 节点），测试中经 `context_search/context_get` 按需读取，不进每轮提示。
- **（二阶段可选）Host 情报注记**：该系统 host 被 worker 锚定时，注入一条 ≤300 字的联盟情报（框架/加密/业务），复用现有 `HostPinnedNote`/`host_lessons.go` 的挂载模式，让"带着描述测试"不额外花一次调用。

## 四、统计

复用四张表 + 现有 tasks/findings：成员单位数、系统数、入口数；平台 / 框架 / 加密算法分布；标签 TOP；**被测覆盖率**（有 `alliance_task_links` 的系统占比）、关联任务数、按严重度的漏洞数。全部只读聚合。

## 五、与上游同步的隔离（"不合并 main"的落地）

新增文件（互不冲突）：`db/alliance.go`、`server/alliance.go`、`web/src/app/(main)/function/alliance/**`、`web/src/components/alliance/**`。

必须触碰的共享文件（全部"加行"改动，作为冲突面清单）：

1. `db/schema.sql` —— 文件末尾 fork-only 区加 4 张表；
2. `server/server.go` —— 注册联盟路由（约 8 行）；
3. `web/src/app/(main)/_components/sidebar/app-sidebar.tsx` —— 导航一项；
4. `web/src/lib/api.ts` —— 联盟接口函数；
5. `web/src/components/tasks/task-creation-form.tsx` —— 联盟选择器；
6. `CHANGELOG.md` —— fork 段；
7. 二阶段注记：`agent/alliance_note.go`（新文件）+ 1 处挂载点。

维护方式：长期分支，定期 rebase main；每次上游同步后核对以上文件的 diff 是否被覆盖。

## 六、分期与验收

- **阶段 1（先做）**：台账 CRUD + 统计 + 新建任务选择器 + 目标简报文本（**不动 agent**）。
- **阶段 2**：按需详情（起点字段/digest）+ Host 情报注记（agent 挂载）。
- 验收：能建成员/系统/入口；新任务选中联盟系统后任务范围出现对应资产且自动批准；任务描述里简报格式正确；统计数字与台账一致；agent 侧能按需读到完整描述。

## 七、待定问题（讨论点）

1. 成员单位：新建 `alliance_members`（建议）还是复用 `companies`？
2. 安全描述：结构化字段（框架/加解密/平台/业务/中间件）+ 自由备注（建议），还是单一自由文本？
3. 一个系统多入口：全部进任务范围，还是默认只进主 URL、其余按需？
4. 要不要"从标准资产库导入"（把已发现资产一键转成联盟条目）？
5. 目标简报预算（10 个系统 / 1500 字）是否符合实际规模？联盟成员/系统大约多少个？
6. 任务列表/详情要不要显示"联盟来源"标签？