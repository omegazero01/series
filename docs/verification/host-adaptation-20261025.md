# 本机环境适配（阶段 1+2）验证记录 — 2026-10-25

分支：`feat/host-adaptation`（worktree：`E:\AI\series\.runtime\feat-host-adaptation`，基于 `main` c8e5082）

## 范围

本次只实现讨论中的前两步：

1. **Bash 工具描述固定注入本机运行环境说明**：解决 Windows 上模型按 bash/cmd 习惯写命令导致的 `&&`、`dir /b`、`2>/dev/null`、`head/tail`、`/tmp`、`curl -m` 等高频错误。说明常驻工具描述，不依赖记忆召回。
2. **asset_search DSL 兼容**：允许运算符两侧空格；`contains`、`=~` 作为模糊匹配 `=` 的同义词；单双引号都按引用值处理；工具描述补齐字段与规则；BARE 错误附带正确示例。

**不在本次范围**（后续阶段）：记忆系统改造（中文分词、索引注入、pinned host lessons、错误签名沉淀）、Windows shell 切换（git-bash/pwsh）、命令重写兼容层、前端 UI。

## 改动文件

| 文件 | 改动 |
|---|---|
| `agent/host_profile.go`（新增） | 只读探测 `GOOS`、Windows PowerShell 版本、`curl` 别名、`curl.exe` 路径；`HostEnvironmentNote()` 用 `sync.Once` 进程内缓存；只读探测命令 3 秒超时；非 Windows 返回空字符串。说明上限 900 字符。 |
| `agent/host_profile_test.go`（新增） | 纯函数用例：Windows 说明包含 PowerShell/版本/`&&`/`$env:TEMP`/`curl.exe`/别名；未知版本时不输出版本；Linux/macOS 返回空。 |
| `server/assembly.go` | Bash 工具描述改为无条件追加 `agent.HostEnvironmentNote() + shellToolNote(...)`；此前只有存在自定义 shell 工具时才追加，且没有本机说明。 |
| `agent/asset_tools.go` | `asset_search` 描述补齐：空格允许、`=`/`==`/`!=`、`contains`/`=~`、字段清单、`path` 用 `url`、`type` 不是 `types`、`limit`/`offset` 续页。 |
| `db/asset_dsl.go` | `tryParseFieldExpr` 支持运算符两侧空格、`contains`、`=~`；新增 `skipDSLSpaces`/`readDSLWord`/`dslWordBoundary`/`readDSLValue`（单双引号）；`ParseDSL` 残留 token 错误附正确示例；更新语法注释。 |
| `db/asset_dsl_parse_test.go`（新增） | 10 组合法写法（含空格、contains、=~、单引号）+ 组合表达式 SQL/参数断言 + 4 组非法写法 + 错误示例提示断言。 |
| `CHANGELOG.md` | Agent 新增“Bash 工具描述固定注入本机运行环境说明”；资产新增“asset_search DSL 兼容空格与 contains/=~”。 |

## 实际验证

环境：Windows，`SERIES_PG_DSN` 指向不可达地址（不碰业务库）。

```text
$ gofmt -w agent/host_profile.go agent/host_profile_test.go agent/asset_tools.go \
         db/asset_dsl.go db/asset_dsl_parse_test.go server/assembly.go
（无输出）

$ SERIES_PG_DSN='postgres://nobody:nobody@127.0.0.1:1/none?sslmode=disable' \
  go test -count=1 ./db -run 'TestParseDSL|TestBuildTaskDSL|TestTaskDSL'
ok  github.com/neouks/series/db  0.166s

$ SERIES_PG_DSN='...' go test -count=1 ./agent -run 'TestListAssetsApprovalDSL|TestBuildHostEnvironmentNote'
ok  github.com/neouks/series/agent  0.163s

$ SERIES_PG_DSN='...' go test -count=1 ./server -run '^$'
ok  github.com/neouks/series/server  6.098s [no tests to run]

$ go vet ./agent ./db
（无输出，exit 0）

$ go build ./...
exit 0
```

### 本机真实探测输出

用临时测试调用 `HostEnvironmentNote()`（验证后已删除该临时文件）：

```text
【本机运行环境｜每轮固定注入】
- 系统：Windows；Bash 工具实际由 Windows PowerShell 执行（-NoProfile -NonInteractive -Command），不是 bash，也不是 cmd。 当前版本 5.1.26100.9711。
- 不要用 && 或 ||；多步命令用 ; 分隔。不要用 dir /b、cd /d、2>nul 这类 cmd 写法。
- 没有 head/tail/grep/wc/export：用 Select-Object -First/-Last、Select-String、Measure-Object、$env:NAME="值"。
- 临时文件不要写 /tmp/，用 $env:TEMP\文件名。
- HTTP 请求用 curl.exe（curl 是 Invoke-WebRequest 的别名：-m 等参数会歧义，且不会加载 .curlrc 默认 UA）；复杂 HTTP 先写 .ps1 或 Python 脚本文件再执行。
- 命令报语法/参数错误时，先按错误改用 PowerShell 写法，不要原样重试。
```

### DSL 兼容用例

`TestParseDSLWhitespaceAndFuzzySynonyms` 覆盖：

```text
domain==example.com
domain == "example.com"
domain = "example.com"
domain=example.com
url contains "userList"
url contains userList
url =~ 'userList'
url =~ userList
port >= 443
domain != "localhost"
```

`TestParseDSLCombinedExpressionKeepsTree` 断言组合式生成同一 WHERE 树：

```text
(domain ILIKE '%example.com%' OR url ILIKE '%api%') AND port = 443
args = ["%example.com%", "%api%", 443]
```

## 阶段 3a：记忆目录、中文检索与安全写入（同一分支追加）

### 发现的 SDK 限制

norma `memory.slug()` 的规则是 `[^a-z0-9]+ → -` 再去首尾 `-`。纯中文标题会得到空 slug，写到 `data/memory/.md`；多个纯中文名会互相覆盖。现有 66 条记忆大多含英文锚点（接口名/参数名/技术名），所以尚未全部撞在一起，但纯中文标题存在覆盖风险。

### 改动

- `agent/memory_tools.go`（新增）：
  - `ListMemories`：列出记忆目录（名称、类型、描述、保存时间），支持中文子串过滤，中文标题无需分词即可看到。
  - `SearchMemories`：英文按 `[a-z0-9_]{2,}` 词、中文按双字滑窗（bigram）检索，返回最相关记忆正文；解决 norma `RecallMemory` 纯中文查询 0 token 召回为空的问题。
  - `ReadMemory`：按名称/slug 读取正文；`Read()` 返回结果与请求名不符时（纯中文名空 slug 导致读到 `.md`）回退全目录扫描，未命中时给相近候选。
  - `RecordMemory`（安全实现，沿用 SDK 工具名）：纯中文名使用 `memory-<sha1前4字节>` 文件名，同名视为更新，不同名同 slug 追加哈希后缀；写完后按 SDK 相同格式重建 `MEMORY.md`。SDK 自带的 `RecordMemory` 不再挂载。
- `agent/worker.go`：worker 绑定上述 5 个工具（`ListMemories`/`ReadMemory`/`SearchMemories`/`RecallMemory`/`RecordMemory`）；系统提示追加一段记忆使用说明（先看目录、命中先读、本机/工具坑解决后用 `RecordMemory` 记录）。
- `agent/memory_tools_test.go`（新增）：中文子串目录过滤、中英文分词、纯中文检索命中、纯中文标题读取回退、两个纯中文名不同文件不覆盖、同名更新、MEMORY.md 重建、5 个工具名。

### 验证输出

```text
$ go test ./agent -run 'TestMemory|TestFindMemory|TestRankMemories|TestSaveMemory|TestBuildHostEnvironmentNote' -v
--- PASS: TestBuildHostEnvironmentNoteWindows
--- PASS: TestBuildHostEnvironmentNoteOmitsUnknownPowershellVersion
--- PASS: TestBuildHostEnvironmentNoteNonWindowsIsEmpty
--- PASS: TestMemoryCatalogSupportsChineseSubstring
--- PASS: TestMemoryQueryTermsHandlesChineseAndEnglish
--- PASS: TestRankMemoriesFindsChineseOnlyMemory
--- PASS: TestFindMemoryFallsBackToChineseTitle
--- PASS: TestMemoryCatalogToolsExposeReadAndSaveTools
--- PASS: TestSaveMemoryFileKeepsChineseNamesDistinct
ok  github.com/neouks/series/agent  3.216s
```

## 阶段 3b：报错自动沉淀与回注（同一分支追加）

### 改动

- `agent/host_lessons.go`（新增）：
  - `RecordHostLesson(tool, input, result)` 在工具失败时分类：Bash 的 `&&` ParserError、curl 别名歧义、缺失命令（head/tail/grep/wc/export/python3/touch/cat/sudo/chmod）、`/dev/null`、`/tmp`、cmd 写法、通用 ParserError；asset_search 的 DSL BARE、未知字段、未知参数。按签名去重计数，写入 `<data>/host/lessons.json`；**不保存原始命令/响应**，避免凭据外泄。
  - `HostLessonNote()` 输出命中次数最高的 6 条经验，注入 Bash 与 asset_search 工具描述；`ConfigureHostLessons("")` 可关闭。
- `agent/sdk_session.go`：`dispatchHooks.PostToolUse` 在 `isErr` 时先调用 `RecordHostLesson`，再转发给 inner hook。
- `server/assembly.go`：Bash 描述追加 `HostEnvironmentNote() + HostLessonNote() + shellToolNote()`。
- `agent/asset_tools.go`：asset_search 描述动态追加 `HostLessonNote()`。
- `server/manager.go`：启动时 `ConfigureHostLessons(<data>/host/lessons.json)`。
- `agent/host_lessons_test.go`（新增）：去重计数、6 类 Bash 错误分类、3 类 DSL 错误分类、超时/网络失败不误记、note 渲染/清空、asset_search 描述携带经验。

### 验证输出

```text
$ go test ./agent -run 'TestRecordHostLesson|TestClassifyHostLesson|TestHostLessonNote|TestAssetSearchDescriptionCarriesHostLessons' -v
--- PASS: TestRecordHostLessonDedupesAndCounts
--- PASS: TestClassifyHostLessonKnownBashFailures
--- PASS: TestClassifyHostLessonDslFailures
--- PASS: TestClassifyHostLessonIgnoresNoise
--- PASS: TestHostLessonNoteRendersAndClears
--- PASS: TestRecordHostLessonWithoutPathIsNoop
--- PASS: TestAssetSearchDescriptionCarriesHostLessons
ok  github.com/neouks/series/agent

$ go build ./...
exit 0
```

## 阶段 3c：pinned 常驻注入、RecordMemory 彻底替换、经验写入去抖（同一分支追加）

### 改动

- **Pinned 常驻注入**：`pinned: true` frontmatter、`type=feedback`、名称以 `host-` 开头的记忆视为常驻；worker 每次 run 的系统提示固定注入一份“常驻记忆”清单（名称 + 一句话描述，最多 8 条 / 900 字符），完整正文仍按需 `ReadMemory`/`RecallMemory`。另按当前意图文本用中英文分词召回最多 3 条相关记忆注入，pinned 不重复出现。没有默认注入全量 `MEMORY.md`（约 4.5k token），目录发现交给 `ListMemories`。
- **彻底替换 SDK 记忆工具**：worker 不再设置 `agentcore.MemoryOptions`；改用 SERIES 自己的 `RecallMemory`/`RecordMemory`（与 SDK 同名，但写文件用名称哈希文件名，纯中文名不再互相覆盖）+ `ListMemories`/`ReadMemory`/`SearchMemories`。用户原来记录的 `RecordMemory` 习惯仍然有效，只是落盘实现变安全了；`SaveMemory` 名称移除。
- **经验写入去抖**：`host/lessons.json` 不再每条错误写一次；`RecordHostLesson` 只更新内存计数，最多每 2 秒批量落盘一次，`HostLessonNote()` 会把未落盘的计数覆盖进展示；`FlushHostLessons()` 可手动强制落盘，`ConfigureHostLessons` 切换路径前自动 flush。

### 验证输出

```text
$ go test ./agent -run 'TestMemory|TestHostPinned|TestMemoryRelevant|TestMemoryPinned|TestRecordMemory|TestHostLessonsDebounce'
--- PASS: TestHostPinnedNoteIncludesFeedbackAndExplicitPinned
--- PASS: TestMemoryRelevantNoteSkipsPinnedMemories
--- PASS: TestMemoryPinnedFrontmatterRoundTrip
--- PASS: TestHostLessonsDebounceDoesNotWriteEveryError
--- PASS: TestMemoryCatalogToolsExposeReadAndSaveTools
ok  github.com/neouks/series/agent

$ go build ./...
exit 0
```

## 未验证 / 待办

- 未跑真实任务端到端验证；只验证了编译、单测和真实本机探测。错误率对比需要拿真实任务前后各一批 transcript 统计。
- 未重新构建/替换 `series.exe`；也未启动或重启 SERIES（用户运行中的实例未受影响）。
- 现有存量纯中文名记忆如果之前已经被 SDK 覆盖，只能看到覆盖后的那一份；本分支只保证新写入不再覆盖。
- 常驻注入给的是名称+描述，不注入全文；如果希望常驻记忆正文也每次可见，需要单独开设置项并评估 token。
- 本机首次工具装配时会执行两次 PowerShell 只读探测（版本 + 别名），有 3 秒超时保护；探测结果进程内缓存。

## 2026-10-26 追加：Windows 下删除 SDK Bash 通用提示

背景：实测 `llm_records.raw_request`，模型看到的 Bash 描述由四段拼成——SDK 短句、本机运行环境说明、本机经验、**SDK 通用提示**。最后一段建议「`&&` 串联命令」「heredoc 传多行脚本」，而本机 Bash 实际由 Windows PowerShell 5.1 执行，两招都会报 ParserError；该段由 `Registry.Schemas()` 拼在描述末尾（Description + "\n\n" + Prompt），位置比本机说明更靠后。10-26 当天复现的解析类失败（`python - <<'PY'`、`python -c "多行"`）全部属于该段引导的写法，且事发 run 的每次请求都带本机经验提示（如 `exp166-worker-i12111` 56/56 次调用均带「命令被 PowerShell 解析失败」）。

改动：
- `agent/toolcatalog.go`：新增 `promptOverrideTool` / `OverrideToolPrompt(t, prompt)`，只覆盖 `Prompt()`，其余方法全部委托。
- `agent/host_profile.go`：新增 `BashPromptOverride()`（Windows 返回空串 + ok=true，非 Windows 返回 ok=false 保持 SDK 原文）。
- `server/assembly.go`：Bash 描述装饰之后按 `BashPromptOverride()` 覆盖扩展提示。

### 验证输出

```text
$ gofmt -l agent/host_profile.go agent/toolcatalog.go server/assembly.go agent/host_profile_test.go
（无输出）

$ go test ./agent -run 'TestBashPromptOverride|TestOverrideToolPrompt|TestRegistrySchemaDropsSDKPrompt' -v -count=1
--- PASS: TestBashPromptOverrideOnlyOnWindows
--- PASS: TestOverrideToolPromptDropsSDKText
--- PASS: TestRegistrySchemaDropsSDKPromptAfterOverride
ok  github.com/neouks/series/agent

$ go build ./agent ./server
build OK
```

### 未验证 / 待办

- `./agent` 全量测试有 3 个先存在的失败（`TestCaptureApprovalLifecycle`、`TestWorkerReviewContextAcrossToolCalls`、`TestRealCurlPicksUpUserAgent`）；已 `git stash` 在基线复跑确认与本次改动无关（环境依赖）。
- 未跑真实任务端到端：需重启实例后抓一次 `llm_records.raw_request`，确认 Bash 描述里 `chain steps with &&` 段消失、本机说明与本机经验仍在。
- 未重新构建/替换 `series.exe`。