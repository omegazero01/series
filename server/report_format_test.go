package server

import (
	"strings"
	"testing"
)

func TestValidateFindingReportAcceptsFixedTemplate(t *testing.T) {
	report := `# XX系统存在SQL注入漏洞

**漏洞名称：** XX系统存在SQL注入漏洞
**漏洞等级：** 高危
**漏洞类型：** SQL注入
**漏洞URL：** ` + "`https://example.test/item?id=1`" + `

## 详细描述
搜索参数被直接拼接进 SQL，攻击者可改写查询条件。

## 复现步骤
1. 发送请求：

` + "```http" + `
GET /item?id=1%27 HTTP/1.1
Host: example.test
` + "```" + `

实际返回数据库错误。

## 修复方案
1. 使用参数绑定。
`
	if err := validateFindingReport(report); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
}

func TestValidateFindingReportRejectsMissingField(t *testing.T) {
	report := `# XX系统存在SQL注入漏洞

**漏洞名称：** XX系统存在SQL注入漏洞
**漏洞等级：** 高危
**漏洞类型：** SQL注入
**漏洞URL：** ` + "`https://example.test/item?id=1`" + `

## 详细描述
搜索参数被直接拼接进 SQL。

## 修复方案
1. 使用参数绑定。
`
	err := validateFindingReport(report)
	if err == nil {
		t.Fatal("report missing 复现步骤 was accepted")
	}
}

func TestValidateFindingReportRejectsStandaloneEvidenceSection(t *testing.T) {
	report := `# XX系统存在SQL注入漏洞

**漏洞名称：** XX系统存在SQL注入漏洞
**漏洞等级：** 高危
**漏洞类型：** SQL注入
**漏洞URL：** ` + "`https://example.test/item?id=1`" + `

## 详细描述
搜索参数被直接拼接进 SQL。

## 复现步骤
1. 发送请求并观察响应。

## 修复方案
1. 使用参数绑定。

## 证据
这里是另外一段证据。
`
	err := validateFindingReport(report)
	if err == nil {
		t.Fatal("standalone evidence section was accepted")
	}
}

func TestSplitFindingReportKeepsTechnicalAppendixPrivate(t *testing.T) {
	raw := "# XX系统存在SQL注入漏洞\n\n" +
		"**漏洞名称：** XX系统存在SQL注入漏洞\n" +
		"**漏洞等级：** 高危\n" +
		"**漏洞类型：** SQL注入\n" +
		"**漏洞URL：** https://example.test/item?id=1\n\n" +
		"## 详细描述\n搜索参数被直接拼接进 SQL。\n\n" +
		"## 复现步骤\n1. 发送请求并观察响应。\n\n" +
		"## 修复方案\n1. 使用参数绑定。\n\n" +
		findingReportTechnicalBegin + "\n" +
		"## 证据\n这里放完整的库表枚举、payload 变体和原始响应。\n" +
		findingReportTechnicalEnd

	if err := validateFindingReport(raw); err != nil {
		t.Fatalf("valid technical appendix rejected: %v", err)
	}
	human, detail := splitFindingReport(raw)
	if strings.Contains(human, "库表枚举") {
		t.Fatalf("technical detail leaked into submission: %s", human)
	}
	if !strings.Contains(detail, "库表枚举") || !strings.Contains(detail, findingReportTechnicalBegin) {
		t.Fatalf("technical detail was not retained: %s", detail)
	}
}

func TestValidateFindingReportRejectsInternalProcessNotes(t *testing.T) {
	report := "# XX系统存在SQL注入漏洞\n\n" +
		"**漏洞名称：** XX系统存在SQL注入漏洞\n" +
		"**漏洞等级：** 高危\n" +
		"**漏洞类型：** SQL注入\n" +
		"**漏洞URL：** https://example.test/item?id=1\n\n" +
		"## 详细描述\n搜索参数被直接拼接进 SQL。\n\n" +
		"## 复现步骤\n1. 发送请求并观察响应。\n\n" +
		"## 流量绑定说明\n本次未绑定流量。\n\n" +
		"## 修复方案\n1. 使用参数绑定。\n"

	if err := validateFindingReport(report); err == nil {
		t.Fatal("internal process note was accepted in submission")
	}
}

func TestValidateFindingReportRejectsNonNormalizedTitleAndType(t *testing.T) {
	report := "# XX系统（sqli.php 接口）存在SQL注入漏洞\n\n" +
		"**漏洞名称：** XX系统（sqli.php 接口）存在SQL注入漏洞\n" +
		"**漏洞等级：** 高危\n" +
		"**漏洞类型：** SQL注入（数字型、UNION）\n" +
		"**漏洞URL：** https://example.test/item?id=1\n\n" +
		"## 详细描述\n搜索参数被直接拼接进 SQL。\n\n" +
		"## 复现步骤\n1. 发送请求并观察响应。\n\n" +
		"## 修复方案\n1. 使用参数绑定。\n"

	if err := validateFindingReport(report); err == nil {
		t.Fatal("non-normalized title/type was accepted")
	}
}

func TestValidateFindingReportRejectsTooManySteps(t *testing.T) {
	report := "# XX系统存在SQL注入漏洞\n\n" +
		"**漏洞名称：** XX系统存在SQL注入漏洞\n" +
		"**漏洞等级：** 高危\n" +
		"**漏洞类型：** SQL注入\n" +
		"**漏洞URL：** https://example.test/item?id=1\n\n" +
		"## 详细描述\n搜索参数被直接拼接进 SQL。\n\n" +
		"## 复现步骤\n" +
		"### 步骤 1\n请求一。\n### 步骤 2\n请求二。\n### 步骤 3\n请求三。\n" +
		"### 步骤 4\n请求四。\n### 步骤 5\n请求五。\n### 步骤 6\n请求六。\n### 步骤 7\n请求七。\n\n" +
		"## 修复方案\n1. 使用参数绑定。\n"

	if err := validateFindingReport(report); err == nil {
		t.Fatal("more than six steps was accepted")
	}
}

// buildBudgetReport assembles a fixed-template report so each length-budget
// case only has to vary one section.
func buildBudgetReport(desc, impact, fix string) string {
	parts := []string{
		"# XX系统存在SQL注入漏洞",
		"",
		"**漏洞名称：** XX系统存在SQL注入漏洞",
		"**漏洞等级：** 高危",
		"**漏洞类型：** SQL注入",
		"**漏洞URL：** https://example.test/item?id=1",
		"",
		"## 详细描述",
		desc,
		"",
		"## 复现步骤",
		"1. 发送请求并观察响应。",
		"",
	}
	if impact != "" {
		parts = append(parts, "## 影响范围", impact, "")
	}
	parts = append(parts, "## 修复方案", fix)
	return strings.Join(parts, "\n")
}

func TestValidateFindingReportLengthBudgets(t *testing.T) {
	pad := func(n int) string { return strings.Repeat("测", n) }
	descOK := "登录接口的账号校验可被匿名调用，服务端直接以业务文案返回账号是否存在。" +
		"该接口在免登录白名单且无频次限制，攻击者可批量枚举有效账号。" +
		"结合无锁定、无验证码的口令校验面，可进一步自动化爆破口令。" +
		"定级依据：可零凭据扩大攻击面并直达后台账号，定为中危。"
	impactOK := "- 受影响对象：平台登录链路与后台账号。\n- 后果：可枚举有效账号并自动化爆破口令。"
	fixOK := "1. 统一认证失败响应，消除账号存在性差分。\n2. 对同一账号与源 IP 做失败计数与锁定。"

	cases := []struct {
		name    string
		desc    string
		impact  string
		fix     string
		wantErr string
	}{
		{"accepts sections within budget", descOK, impactOK, fixOK, ""},
		{"accepts report without impact section", descOK, "", "1. 使用参数绑定。", ""},
		{"rejects over-long description", descOK + pad(260), impactOK, fixOK, "详细描述最多 350 字"},
		{"rejects description mega-sentence", "位置：登录接口。" + pad(150) + "。", impactOK, fixOK, "详细描述单句最长 120 字"},
		{"rejects too many description sentences", "一。二。三。四。五。", impactOK, fixOK, "详细描述最多 4 句"},
		{"rejects too many impact items", descOK, "- 一。\n- 二。\n- 三。\n- 四。\n- 五。", fixOK, "影响范围最多 4 条"},
		{"rejects over-long impact item", descOK, "- " + pad(61), fixOK, "影响范围第 1 条"},
		{"rejects over-long impact total", descOK,
			"- " + pad(55) + "\n- " + pad(55) + "\n- " + pad(55) + "\n- " + pad(55), fixOK, "影响范围最多 200 字"},
		{"rejects too many fix items", descOK, impactOK, "1. 一。\n2. 二。\n3. 三。\n4. 四。\n5. 五。", "修复方案最多 4 条"},
		{"rejects over-long fix item", descOK, impactOK, "1. " + pad(121), "修复方案第 1 条"},
		{"rejects over-long fix total", descOK, impactOK,
			"1. " + pad(110) + "\n2. " + pad(110) + "\n3. " + pad(110) + "\n4. " + pad(110), "修复方案最多 400 字"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFindingReport(buildBudgetReport(tc.desc, tc.impact, tc.fix))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("valid report rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
