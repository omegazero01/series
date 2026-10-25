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

## 未验证 / 待办

- 未跑真实任务端到端验证；只验证了编译、单测和真实本机探测。错误率对比需要拿真实任务前后各一批 transcript 统计。
- 未重新构建/替换 `series.exe`；也未启动或重启 SERIES（用户运行中的实例未受影响）。
- 记忆系统阶段（中文分词、索引、pinned host lessons、错误签名沉淀）尚未实现。
- 本机首次工具装配时会执行两次 PowerShell 只读探测（版本 + 别名），有 3 秒超时保护；探测结果进程内缓存。