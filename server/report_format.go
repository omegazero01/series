package server

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The reporter writes one Markdown document. Everything outside this HTML-comment
// block is the human submission; the block itself is retained only in
// findings.report_detail for context_search / internal review. HTML comments are
// deliberate: if a stale client renders the raw document before splitting, the
// technical appendix stays invisible.
const (
	findingReportTechnicalBegin = "<!-- SERIES:TECHNICAL:BEGIN -->"
	findingReportTechnicalEnd   = "<!-- SERIES:TECHNICAL:END -->"
)

// findingReportRequiredFields are exact labels in the human submission. Requiring
// the labels (not just the words) rejects the previous free-form report shape,
// where headings like "## 漏洞名称" could pass a substring check.
var findingReportRequiredFields = []string{
	"**漏洞名称：**",
	"**漏洞等级：**",
	"**漏洞类型：**",
	"**漏洞URL：**",
	"## 详细描述",
	"## 复现步骤",
	"## 修复方案",
}

var (
	findingReportTechnicalBlock  = regexp.MustCompile(`(?s)\n?<!-- SERIES:TECHNICAL:BEGIN -->.*?<!-- SERIES:TECHNICAL:END -->\n?`)
	findingReportEvidenceHeading = regexp.MustCompile(`(?m)^#{1,3}\s*证据\s*$`)
	findingReportInternalNote    = regexp.MustCompile(`(?m)^#{1,3}\s*(流量绑定说明|克制说明)\s*$`)
	findingReportStepHeading     = regexp.MustCompile(`(?m)^(?:#{2,4}\s*)?(?:\*\*)?步骤\s*[0-9一二三四五六七八九十]`)
	findingReportListItem        = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+[.)、])\s+(.*)$`)
)

// Length budgets for the human submission. The fixed template alone does not
// stop a report from packing the same evidence into three sections: the
// reviewer needs location/cause/impact in 详细描述, the blast radius in
// 影响范围 and actionable steps in 修复方案; everything else belongs in the
// technical appendix (kept for context_search, never exported). Calibrated
// 2026-10-26 against two real exports whose 详细描述 ran 512–648 runes,
// 影响范围 720–1059 and 修复方案 707–755 — roughly half of that repeated
// 复现步骤 or belonged in the appendix.
const (
	findingReportDescRunes         = 350 // 详细描述上限（字）
	findingReportDescSentenceMax   = 4   // 详细描述句数上限
	findingReportDescSentenceRunes = 120 // 详细描述单句上限（防“大句”绕过句数）
	findingReportImpactRunes       = 200 // 影响范围合计上限（字）
	findingReportImpactItemMax     = 4   // 影响范围条数上限
	findingReportImpactItemRunes   = 60  // 影响范围单条上限（字）
	findingReportFixRunes          = 400 // 修复方案合计上限（字）
	findingReportFixItemMax        = 4   // 修复方案条数上限
	findingReportFixItemRunes      = 120 // 修复方案单条上限（字）
)

func reportLabelValue(report, label string) string {
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, label) {
			return strings.TrimSpace(strings.TrimPrefix(line, label))
		}
	}
	return ""
}

func reportH1(report string) string {
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func reportSection(report, heading string) string {
	lines := strings.Split(report, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == heading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func countSentenceEndings(text string) int {
	return strings.Count(text, "。") + strings.Count(text, "！") + strings.Count(text, "？") + strings.Count(text, "!") + strings.Count(text, "?")
}

// reportSentenceLengths returns the rune length of each sentence-like segment,
// breaking on sentence punctuation AND on newlines. Semicolons do not end a
// sentence, so one mega-clause could otherwise satisfy “≤4 句” while carrying a
// whole paragraph; the per-segment cap closes that gap.
func reportSentenceLengths(text string) []int {
	var out []int
	cur := 0
	for _, r := range text {
		switch r {
		case '\n':
			if cur > 0 {
				out = append(out, cur)
				cur = 0
			}
		case '。', '！', '？', '!', '?':
			out = append(out, cur+1)
			cur = 0
		default:
			cur++
		}
	}
	if cur > 0 {
		out = append(out, cur)
	}
	return out
}

// reportListItems returns the text of bullet / numbered lines (any indent).
// Nested items count too: an interface list belongs in the technical appendix,
// not in 影响范围.
func reportListItems(section string) []string {
	ms := findingReportListItem.FindAllStringSubmatch(section, -1)
	items := make([]string, 0, len(ms))
	for _, m := range ms {
		items = append(items, strings.TrimSpace(m[1]))
	}
	return items
}

func validateFindingReportDescribe(human string) error {
	desc := reportSection(human, "## 详细描述")
	if desc == "" {
		return fmt.Errorf("详细描述为空")
	}
	if n := utf8.RuneCountInString(desc); n > findingReportDescRunes {
		return fmt.Errorf("详细描述最多 %d 字（当前 %d 字）：只写位置、原因、影响，可用一句说明定级依据；内网地址、组件版本、清单、字节数等细节放技术附录", findingReportDescRunes, n)
	}
	if n := countSentenceEndings(desc); n > findingReportDescSentenceMax {
		return fmt.Errorf("详细描述最多 %d 句（当前 %d 句）：把过程、枚举与验证细节放技术附录", findingReportDescSentenceMax, n)
	}
	for _, n := range reportSentenceLengths(desc) {
		if n > findingReportDescSentenceRunes {
			return fmt.Errorf("详细描述单句最长 %d 字（当前有 %d 字的长句）：拆成短句，细节放技术附录", findingReportDescSentenceRunes, n)
		}
	}
	return nil
}

func validateFindingReportImpact(human string) error {
	impact := reportSection(human, "## 影响范围")
	if impact == "" {
		return nil // 可选段落
	}
	if n := utf8.RuneCountInString(impact); n > findingReportImpactRunes {
		return fmt.Errorf("影响范围最多 %d 字（当前 %d 字）：只写受影响对象与后果；接口清单、版本号、枚举结果、未验证细节放技术附录", findingReportImpactRunes, n)
	}
	items := reportListItems(impact)
	if len(items) > findingReportImpactItemMax {
		return fmt.Errorf("影响范围最多 %d 条（当前 %d 条）：只留受影响的系统与后果，其余清单放技术附录", findingReportImpactItemMax, len(items))
	}
	for i, item := range items {
		if n := utf8.RuneCountInString(item); n > findingReportImpactItemRunes {
			return fmt.Errorf("影响范围第 %d 条 %d 字，超过每条 %d 字：一句话写完，细节放技术附录", i+1, n, findingReportImpactItemRunes)
		}
	}
	return nil
}

func validateFindingReportFix(human string) error {
	fix := reportSection(human, "## 修复方案")
	if fix == "" {
		return fmt.Errorf("修复方案为空")
	}
	if n := utf8.RuneCountInString(fix); n > findingReportFixRunes {
		return fmt.Errorf("修复方案最多 %d 字（当前 %d 字）：最多 %d 条、每条一两句动作+目标状态；配置样例、阈值参数、事后处置清单放技术附录", findingReportFixRunes, n, findingReportFixItemMax)
	}
	items := reportListItems(fix)
	if len(items) > findingReportFixItemMax {
		return fmt.Errorf("修复方案最多 %d 条（当前 %d 条）：合并同类动作，实施细节放技术附录", findingReportFixItemMax, len(items))
	}
	for i, item := range items {
		if n := utf8.RuneCountInString(item); n > findingReportFixItemRunes {
			return fmt.Errorf("修复方案第 %d 条 %d 字，超过每条 %d 字：一条只写动作与目标状态", i+1, n, findingReportFixItemRunes)
		}
	}
	return nil
}

func validateFindingReportMeta(human string) error {
	allowedLevels := map[string]bool{"严重": true, "高危": true, "中危": true, "低危": true}
	if level := reportLabelValue(human, "**漏洞等级：**"); !allowedLevels[level] {
		return fmt.Errorf("漏洞等级必须是：严重/高危/中危/低危")
	}
	url := reportLabelValue(human, "**漏洞URL：**")
	if url == "" {
		return fmt.Errorf("漏洞URL为空")
	}
	if strings.ContainsAny(url, "【】[]") {
		return fmt.Errorf("漏洞URL直接写完整 URL，不要加【】或[]")
	}
	return nil
}

func validateFindingReportTitle(human string) error {
	name := reportLabelValue(human, "**漏洞名称：**")
	if name == "" {
		return fmt.Errorf("漏洞名称为空")
	}
	if h1 := reportH1(human); h1 != "" && h1 != name {
		return fmt.Errorf("H1 标题必须与“漏洞名称”字段完全一致")
	}
	lower := strings.ToLower(name)
	if strings.Contains(lower, "http") || strings.Contains(lower, ".php") || strings.Contains(lower, ".jsp") || strings.Contains(lower, ".aspx") || strings.ContainsAny(name, "/?=【】[]") {
		return fmt.Errorf("漏洞名称不要包含接口、文件名、URL、参数或括号解释；归纳为“系统名 + 存在 + 漏洞类型 + 漏洞”")
	}
	if strings.Contains(name, "接口") || strings.Contains(name, "参数") {
		return fmt.Errorf("漏洞名称不要包含“接口/参数”；归纳为“系统名 + 存在 + 漏洞类型 + 漏洞”")
	}
	return nil
}

func validateFindingReportType(human string) error {
	vulnType := reportLabelValue(human, "**漏洞类型：**")
	if vulnType == "" {
		return fmt.Errorf("漏洞类型为空")
	}
	if strings.ContainsAny(vulnType, "（(、,，") || strings.Contains(vulnType, "+") {
		return fmt.Errorf("漏洞类型只写标准类型（例如 SQL注入），不要写数字型、UNION、盲注等利用细节")
	}
	return nil
}

func validateFindingReportBody(human string) error {
	if err := validateFindingReportDescribe(human); err != nil {
		return err
	}
	steps := reportSection(human, "## 复现步骤")
	if steps == "" {
		return fmt.Errorf("复现步骤为空")
	}
	if n := len(findingReportStepHeading.FindAllStringIndex(steps, -1)); n > 6 {
		return fmt.Errorf("复现步骤最多 6 步；把备用通道和枚举细节放技术附录")
	}
	return nil
}

// splitFindingReport returns the human submission and the full technical detail.
// The detail always retains the original document; the human projection removes
// the internal technical block. Legacy reports without a block project to the
// same text, so upgrading existing findings cannot silently drop content.
func splitFindingReport(raw string) (human, detail string) {
	detail = strings.TrimSpace(raw)
	if detail == "" {
		return "", ""
	}
	human = strings.TrimSpace(findingReportTechnicalBlock.ReplaceAllString(detail, "\n"))
	if human == "" {
		// A document containing only a technical block is not a submittable
		// report. Keep it as the human fallback so validation can explain the
		// missing fields instead of replacing the content with an empty string.
		human = detail
	}
	return human, detail
}

// validateFindingReport is intentionally structural rather than semantic: it
// catches missing sections and the old standalone evidence dump without trying
// to judge whether the vulnerability is real. Evidence must live inside the
// reproduction request/response packets, not in a separate report section.
func validateFindingReport(report string) error {
	human, _ := splitFindingReport(report)
	if human == "" {
		return fmt.Errorf("报告为空")
	}
	if utf8.RuneCountInString(human) < 80 {
		return fmt.Errorf("报告过短，至少需要包含漏洞信息和可复现步骤")
	}
	var missing []string
	for _, field := range findingReportRequiredFields {
		if !strings.Contains(human, field) {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("报告缺少字段: %s", strings.Join(missing, "、"))
	}
	if findingReportEvidenceHeading.MatchString(human) {
		return fmt.Errorf("报告不要单独写“证据”段；请求包和响应包放在“复现步骤”中")
	}
	if findingReportInternalNote.MatchString(human) {
		return fmt.Errorf("报告正文不要写“流量绑定说明”或“克制说明”；技术过程请放入技术附录")
	}
	if err := validateFindingReportMeta(human); err != nil {
		return err
	}
	if err := validateFindingReportTitle(human); err != nil {
		return err
	}
	if err := validateFindingReportType(human); err != nil {
		return err
	}
	if err := validateFindingReportBody(human); err != nil {
		return err
	}
	if err := validateFindingReportImpact(human); err != nil {
		return err
	}
	if err := validateFindingReportFix(human); err != nil {
		return err
	}
	return nil
}
