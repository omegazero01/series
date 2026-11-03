# 联盟画像靶场验证：Vulinbox `/user/id`

- 日期：2026-11-03
- 环境：`E:\AI\SCCBA`（容器 `sccba-profile-series-1`，版本 `4473ee4`）
- 靶场：`http://127.0.0.1:9999/`（Vulinbox - Agent，本地授权）
- 选用场景：`GET /user/id?id=1` 数字型 SQL 注入
- 模型：SCCBA 模型配置 #1，`MiniMax-M3.1-Flash`，已激活；连接测试返回 `OK`

## 一、画像

- 系统 id：**3**
- 名称：Vulinbox 数字型 SQL 注入（容器内 `host.docker.internal:19999`）
- 画像版本：**v4**
- 34 个字段均非空：20 supplied、10 not_applicable、3 unknown（带说明）、1 confirmed
- 入口：`http://host.docker.internal:19999/user/id?id=1`

说明：靶场在 Windows 宿主机的 `127.0.0.1:9999`，SCCBA 在 Docker 容器里，容器里的 `127.0.0.1` 是容器自身。为了测试，宿主机临时起了一个 `0.0.0.0:19999 -> 127.0.0.1:9999` 的 TCP 转发；测试结束后已停止。

## 二、画像是否生效

### 任务 1：原始 127.0.0.1 地址

- 任务 id：**1**
- 画像阅读状态：`ready`
- 阅读结果准确复述了：
  - `system_id=3`、名称、画像版本 2；
  - 入口 `http://127.0.0.1:9999/user/id?id=1`；
  - 范围仅本机、只读、不写库、不扩展主机/端口；
  - “画像声明待运行确认”，没有把画像标签当成已证实漏洞。

**结论：画像读取和注入生效。** 但 worker 在容器内访问 `127.0.0.1:9999` 被拒绝，任务 1 暂停，避免继续消耗 token。

### 任务 2：容器可达地址

- 任务 id：**2**
- 目标：`http://host.docker.internal:19999/user/id?id=1`
- 画像阅读状态：`failed`，错误 `画像阅读输出不完整`
- 但 worker 仍然拿到了画像文档：worker 在推理中明确指出“画像说 id=1' 返回 500，实测是 200”，说明即使阅读摘要失败，结构化画像仍通过 `RenderContext` 注入。
- worker 遵守了画像中的边界：
  - 只访问 `/user/id`，没有扫描其他主机/端口；
  - 使用 `ProxyHandler({})` 绕过 `127.0.0.1:8788` 平台代理；
  - 请求只读，没有写库。

**实际验证结果：**

- `id=1`：200，正常用户列表；
- `id=1'`：200，`unrecognized token: "';"`，SQLite 词法错误；
- `id=1 AND 1=1`：与基线相同；
- `id=1 AND 1=2`：返回 `null`；
- `id=2-1`：等价于 `id=1`；
- `id=0 UNION SELECT ...`：可执行，读出 SQLite `3.51.2`、9 列、26 个用户及明文口令；
- 最终 `report_finding` 生成 **high** 级发现：
  - `/user/id 接口 id 参数数字型 SQL 注入（SQLite UNION SELECT 可读取全库）`
  - 状态 `pending`（等待人工审核）

**结论：画像内容确实影响了 agent 的目标、地址、范围和测试方式，画像生效。**

## 三、测出的 bug

### BUG 1（高优先级）：画像阅读/反哺的输出上限 4096 太小

证据：

- 任务 2 阅读：`画像阅读输出不完整`
- 反哺 run `id=1`：`state=failed`，`error=画像处理输出不完整，请重试`
- 任务 2 的 finding 已确认，但没有生成任何画像建议（`alliance_profile_suggestions` 0 条）

原因方向：

- `server/alliance_profile.go` 的 `allianceReadMaxOutput = 4096`；
- `MiniMax-M3.1-Flash` 会输出 thinking 内容，较大的画像/证据会把 4096 token 预算耗在思考上，`stop=max_tokens`，被代码判为“输出不完整”；
- 任务 1 的画像较小，阅读成功；任务 2 画像 34 项全非空，阅读和反哺都失败。

建议：把阅读/反哺的输出上限提高到 8192 或 12288，并记录 `stop` 原因和实际输出长度；随后重跑任务 2 的反馈。不要在未验证前直接改成无上限。

### BUG 2：画像字段会覆盖台账列，语义被污染

实测系统 3 的扁平列被 `SaveAllianceProfile` 同步成画像字段：

- `S1-03`（对接人/停止联系人）→ `owner_unit`
- `S1-04`（重点功能与首轮目标）→ `business`
- `S1-08`（技术栈）→ `framework`
- `S2-04`（服务组件/存储）→ `security_note`
- `S2-12`（加解密）→ `crypto`

因此：

- `owner_unit` 变成“本地靶场维护方…；无外部对接人…”；
- `business` 变成“用户查询接口…；首轮目标…”，不是纯业务描述；
- 任务描述里的“业务”字段也被这段目标文本带偏。

建议：要么让这些画像字段只做展示、不回写扁平列；要么在回写前做字段语义校验/截断，并明确哪些字段允许回写。

### BUG 3：任务描述仍用旧标题「联盟系统情报」

任务 1/2 的描述和 worker 用户消息里仍然是：

```text
【联盟系统情报】来自联盟台账，测试时可参考以下背景（完整信息见「联盟系统」页）
```

但 UI 已改名为「联盟画像」。这是残留文案，建议统一改为「联盟画像」，并修正“见「联盟系统」页”的指路。

### BUG 4：画像写 `127.0.0.1`，任务在容器里不可达

画像允许保存 `http://127.0.0.1:9999/...`，但 SCCBA 跑在 Docker 里，worker 访问的是容器自己的 `127.0.0.1`，任务 1 因此空转并暂停。

建议：保存画像时，如果检测到 URL 是 `127.0.0.1`/`localhost`，提示“当前实例在容器中，任务运行时 127.0.0.1 指容器自身；请改用容器可达地址或配置代理”。这是提示，不要硬拦。

### BUG 5（待确认）：任务 DTO 的目标/发现统计不更新

- 任务 2 的图里有 1 个 goal、1 个 finding，但 `/api/tasks/2` 返回 `goals_total=0`、`goals_met=0`、`findings` 全 0；
- 任务最终 `timeout`，goal 仍 `open`，finding 已 `confirmed` 但状态 `pending`；
- 这可能是任务统计/收尾逻辑的问题，不一定是画像本身；需要单独复核。

### 其他观察

- 任务 2 最终超时（约 8 分钟）后才收尾，finding 已写入但目标未自动标记完成；
- agent 读取了 26 个用户和明文口令，超出“最小只读验证”的预期；画像里应更明确写“只验证注入存在，不枚举全表/不读取凭据”，否则模型可能过度收集；
- 任务 1 已暂停，任务 2 已超时，目前没有继续消耗 token。

## 四、复核位置

- 系统：`/api/alliance/systems/3`
- 画像：`/api/alliance/systems/3/profile`
- 任务 1：`/api/tasks/1`、`/api/tasks/1/alliance-profile`
- 任务 2：`/api/tasks/2`、`/api/tasks/2/alliance-profile`
- 反哺 run：`alliance_profile_feedback_runs where task_id=2`
- finding：`findings where task_id=2`
- 任务 2 的 worker transcript：`E:\AI\SCCBA\data\transcripts\exp2-worker-i7.jsonl`

## 五、结论

- **画像生效：是。** 读取摘要、结构化注入、范围/边界引导、目标地址和代理绕过都实际影响了 agent。
- **画像有 bug：有。** 最严重的是阅读/反哺 4096 输出上限导致反哺失败；其次是画像字段覆盖台账列、旧文案残留、容器内 127.0.0.1 不可达提示缺失。
- 本次没有修改源码；只创建了测试画像和任务。测试用端口转发已停止。
