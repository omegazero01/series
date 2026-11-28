package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// missingFindingReports lists findings whose human submission report is empty.
// It powers the findings page "补出提交报告" dialog and uses the same filters as
// the list, so a task/severity narrowed view sees the matching gaps.
func (s *Server) missingFindingReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := findingPaginationParam(q.Get("page"), 1, 0)
	limit := findingPaginationParam(q.Get("limit"), 50, 200)
	filter := findingFilterFromQuery(q)
	filter.SummaryOnly = true
	filter.ReportMissing = true
	fs, total, err := s.m.pg.ListFindingsPage(filter, page, limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	assets := s.resolveFindingAssets(fs)
	out := make([]FindingDTO, 0, len(fs))
	for _, f := range fs {
		out = append(out, findingFromDB(f, assets))
	}
	writeJSON(w, 200, map[string]any{
		"items":     out,
		"total":     total,
		"page":      page,
		"page_size": limit,
	})
}

type generateReportsRequest struct {
	FindingIDs []string `json:"finding_ids"`
}

// generateFindingReports enqueues one reporter run per selected finding. It is
// intentionally separate from report_finding: re-issuing a report must never
// create a duplicate finding. The reporter writes the existing row through
// update_finding_report, and the normal task LLM pin is applied in
// triggerLLMProfilePin.
func (s *Server) generateFindingReports(w http.ResponseWriter, r *http.Request) {
	var req generateReportsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeErr(w, 400, "请求格式错误: "+err.Error())
		return
	}
	if len(req.FindingIDs) == 0 {
		writeErr(w, 400, "至少选择一个漏洞")
		return
	}
	if len(req.FindingIDs) > 200 {
		writeErr(w, 400, "一次最多出具 200 个漏洞的报告")
		return
	}
	reporter, err := s.m.pg.GetAgentByKey("reporter")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if reporter == nil || !reporter.Enabled {
		writeErr(w, 409, "报告撰写 Agent 不存在或未启用")
		return
	}
	if s.chatAgentRef() == nil {
		writeErr(w, 503, "报告运行器尚未就绪")
		return
	}

	queued := 0
	skipped := make([]map[string]string, 0)
	seen := make(map[int64]bool)
	for _, raw := range req.FindingIDs {
		id, parseErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if parseErr != nil || id <= 0 {
			skipped = append(skipped, map[string]string{"finding_id": raw, "reason": "无效的 finding_id"})
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		f, getErr := s.m.pg.GetFinding(id)
		if getErr != nil {
			skipped = append(skipped, map[string]string{"finding_id": strconv.FormatInt(id, 10), "reason": getErr.Error()})
			continue
		}
		if f == nil {
			skipped = append(skipped, map[string]string{"finding_id": strconv.FormatInt(id, 10), "reason": "漏洞不存在"})
			continue
		}
		if f.NodeID == nil {
			skipped = append(skipped, map[string]string{"finding_id": strconv.FormatInt(id, 10), "reason": "漏洞没有探索节点，无法写回报告"})
			continue
		}
		if err := s.enqueueManualFindingReportJob(r.Context(), f); err != nil {
			skipped = append(skipped, map[string]string{
				"finding_id": strconv.FormatInt(id, 10),
				"reason":     err.Error(),
			})
			continue
		}
		queued++
	}
	writeJSON(w, 200, map[string]any{
		"queued":  queued,
		"skipped": skipped,
	})
}
