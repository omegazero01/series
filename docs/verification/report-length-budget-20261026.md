# 提交报告字数预算（2026-10-26）

## 背景

`E:\干活\其他\2026-8-15潍柴子公司渗透测试\` 下两份新导出的提交报告（电池溯源平台 Actuator 未授权访问、用户枚举与口令暴力破解防护缺失）按模板合格，但三段长度失控：

| 段落 | 报告 1 | 报告 2 | 旧约束 |
| --- | ---: | ---: | --- |
| 详细描述 | 648 字 / 4 句（平均 162 字/句） | 512 字 / 4 句 | ≤1200 字、≤5 句末符 |
| 影响范围 | 720 字 / 7 条（最长 135 字） | 1059 字 / 8 条（最长 253 字） | 无 |
| 修复方案 | 755 字 / 3 条（每条 200+ 字） | 707 字 / 3 条 | 无 |

人工核对：三段里约一半内容是复现步骤或内部细节的重复（内网地址、组件版本清单、账号枚举结果、配置样例、阈值建议、事后处置清单）；真正“位置/原因/后果”的必要核心约 200–250 字。旧约束只挡“缺段/独立证据段”，挡不住把证据重抄三遍。

## 改动

- `server/report_format.go`：新增预算常量与三段校验 `validateFindingReportDescribe` / `validateFindingReportImpact` / `validateFindingReportFix`；新增 `reportSentenceLengths`（按句末符与换行分句，防“一句写 300 字”绕过句数上限）与 `reportListItems`（任意缩进的列表项都计数）。
- `agent/promptcatalog.go`：`ReporterDefaultPrompt` 三段写明预算与“细节放附录”，补一句“字数预算只管三段，复现步骤不限、证据必须完整”。
- `server/orchestration.go`：`update_finding_report` 工具说明写入三段预算；`reseedReporterPrompt` 从 v1 改为 `reporter_prompt_src_template_v2` 一次性升级。
- `agent/context_prompt_migration.go`：登记上一版 reporter 默认提示词哈希 `b1549aff…`，v2 升级据此识别“仍是旧默认”的库，用户改过的提示词不动。

## 验证

```text
gofmt -l server agent                                  无输出
go vet ./server ./agent                                exit 0
go build ./agent ./server ./report ./db ./cmd/series   exit 0
go test -count=1 ./report                              ok
go test -count=1 ./server -run 'TestValidateFindingReport|TestSplitFindingReport'
                                                       ok（新增 TestValidateFindingReportLengthBudgets，11 个用例：三段各档超限即拒、限额内放行、无影响范围也放行）
go test -count=1 ./agent -run 'TestContextPromptMetrics|TestChineseLanguagePolicyIdempotent|TestReportFindingAtomicContract|TestSkipPromptScopesAndStableContent|TestPrompt'
                                                       ok
```

数据库依赖用例（`TestReporterDefaultPromptUpgradeUsesCurrentParameter`、`TestFindingTraffic*` 等）需要 `SERIES_PG_DSN` 或 `server/config.json`，本机未配置，未执行——与本次改动无关的环境限制。

## 未做

- 同日评审发现的其它问题不在本次范围：步骤计数正则仍只认“步骤N”（`**1.**` 编号不计步数）；漏洞类型校验仍放行 ` / `（复合类型）；未约束“备注”类额外段落；ZIP 条目时间戳仍为零值（1980-00-00）。
- 已导出的旧报告不改写；重新生成报告时才按新预算执行。
- 运行中的 `series.exe` 未重启；v2 提示词升级在下次启动时生效。