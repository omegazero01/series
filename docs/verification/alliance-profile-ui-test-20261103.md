# 联盟画像 UI 与边界验证（第二轮）

- 日期：2026-11-03
- 环境：`E:\AI\SCCBA`，容器版本 `d032bf2`；浏览器通过本地签发的测试 JWT 登录
- 数据：系统 #3（Vulinbox），当前画像 v7；任务 #2（快照 v4）；任务 #3（无画像）
- 范围：画像页、任务详情页、建议审核、版本冲突、边界校验、无画像路径

## 一、通过的项

| 项目 | 实测结果 |
|---|---|
| 画像建议页签 | 初始显示 19 条待审核；拒绝建议 #20 后变为 18 条，数据库 `state=rejected` |
| 画像编辑器打开位置 | 打开时自动落在「深入测试」阶段（第一个未完成阶段），不是固定第一阶段 |
| 有效已填统计 | 显示 `有效已填 31/34 · 待确认说明 3 · 未填 0`；阶段显示 `8/8`、`15/16`、`8/10` |
| dirty 跟踪 | 修改字段后底部从「没有修改」变为「有未保存修改」，保存按钮从禁用变为可用 |
| 保存 | 从 UI 保存后生成 v6；台账 `owner_unit/business` 保持短值，没有被画像覆盖 |
| 版本历史 | 弹层列出 v1–v6，包含 `manual` / `feedback:19/task:2` 来源和时间 |
| 并发冲突 | 在编辑 v6 时通过 API 接受建议生成 v7；再点保存返回「画像版本冲突：已被他人更新，请点「重新读取」后再保存」，没有覆盖 v7 |
| 重新读取 | 冲突后点「重新读取」拉到 v7，未保存的本地修改被丢弃，保存按钮恢复禁用 |
| loopback 提示 | 新建画像表单输入 `http://127.0.0.1:9999/` 后，地址下方出现容器内不可达提示 |
| 任务快照固定 | 当前画像 v7，任务 #2 的快照仍是 v4；阅读结果也明确写 `profile_version=4` |
| 无画像任务 | 任务 #3 不选联盟画像可以创建；立即暂停后 `snapshots=null`、`reading=null`，没有被强制绑定 |
| 阅读摘要 | 任务详情「查看摘要」可以展开阅读结果；内容带 `profile_version=4` |
| 建议证据 | 「查看证据」可以打开证据弹层，显示 fact #9/#10 的原文 |
| 字段上限 | 单字段 9000 字节返回 400「字段名称或内容超出限制: S2-04」；版本保持 v7 |
| 文档上限 | 文档超过 64 KiB 返回 400「画像正文不能超过 64 KiB」；版本保持 v7 |

## 二、新发现的 bug

### BUG 6（高）：任务详情「查看完整快照」把页面打崩

复现：

1. 打开 `/function/tasks/detail?id=2`；
2. 点「联盟画像」页签；
3. 点「查看完整快照」。

结果：页面变成 `This page couldn't load / Reload / Back`，控制台报：

```text
Uncaught TypeError: Cannot read properties of undefined (reading 'sections')
```

原因：

- 后端 `TaskAllianceSnapshot.Snapshot` 是 `[]byte`，JSON 序列化后 `snapshot` 是**字符串**；
- 前端 `AllianceTaskSnapshot.snapshot` 被声明为 `AllianceSnapshotDocument`（对象），`AllianceProfileDocumentView` 直接读 `s.snapshot.document.sections`；
- 实际拿到的是字符串，`.document` 为 `undefined`，组件崩溃。

证据：

```json
{
  "system_id": 3,
  "system_name": "...",
  "profile_version": 4,
  "snapshot": "{\"id\":3,\"name\":\"...\",\"version\":4,\"document\":{...}}"
}
```

修复方向：

- 后端把 `Snapshot` 改成 `json.RawMessage`（或自定义 `MarshalJSON`），让 `snapshot` 直接输出对象；
- 或前端在渲染前 `JSON.parse` 字符串，并加空值兜底；
- 无论选哪种，补一个接口契约测试，避免类型再次漂移。

### BUG 7（低）：阅读摘要与当前证据不一致

任务 #2 的阅读摘要仍写「数据库疑似 SQLite，版本未知」「当前证据尚无」，但任务已经产出了 SQLite 3.51.2、26 个账号和明文口令等证据。

这不是数据丢失，而是阅读摘要是任务开始时的静态快照；建议在 UI 上标成「任务开始时阅读」，或说明它不会被后续证据自动更新，避免用户误以为摘要过期是 bug。

### BUG 6 修复复测（提交 `4e9609b`）

- `TaskAllianceSnapshot.Snapshot` 从 `[]byte` 改为 `json.RawMessage`，接口现在返回 JSON 对象，不再是字符串；
- 新增契约测试 `TestTaskAllianceSnapshotJSONIsObject`；
- 重新部署后实测：任务 #2 的 `snapshot` 类型为 `dict`，包含 `document`；
- 浏览器点「查看完整快照」不再崩溃，按钮变为「收起快照」，页面正常展示 S1-01、S1-02、S1-03 等快照字段；
- 容器版本 `4e9609b`，`/api/health` 200。

### 已知未修

- BUG 5：任务详情统计的 `goals/findings` 仍为 0（任务 #2 实际有 goal 和 finding）；
- `not_applicable` 无原因时后端仍允许保存（仅前端提示）；
- BUG 7 阅读摘要的「任务开始时」提示未改；
- 本轮测试后已修 BUG 6；画像版本 v7 是接受建议 #18 造成的测试数据变化。

## 三、结论

- 画像编辑、统计、版本历史、并发冲突、循环地址提示、快照固定、无画像路径、字段/文档上限均通过实测。
- 任务详情「查看完整快照」的崩溃已修复并实测通过；根因是接口把快照序列化成了字符串，前端按对象读取。
- 目前仍未修的是 BUG 5（任务统计）和 BUG 7（摘要时间标记），可以后续单独处理。
