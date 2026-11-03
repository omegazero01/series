package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/neouks/series/db"
)

// =====================================================================
// 联盟系统 HTTP API（城商行联盟定制，feat/alliance-system，不合并上游）
//
// 台账 CRUD + 统计 + 任务创建时的目标解析。路由在 server.go 注册：
// /api/alliance/*；新建任务通过 createTaskReq.alliance_system_ids 接入。
// =====================================================================

func (s *Server) allianceStore() *db.AllianceStore {
	if s.m.pg == nil {
		return nil
	}
	return s.m.pg.Alliance()
}

// maxAllianceTaskSystems bounds how many systems one task may target: each
// system contributes endpoints (assets) and a briefing line, and the brief is
// capped anyway.
const maxAllianceTaskSystems = 20

// normalizeAllianceSystemIDs drops invalid / duplicate ids, keeping order.
func normalizeAllianceSystemIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func alliancePathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("id 无效")
	}
	return id, nil
}

func (s *Server) listAllianceSystems(w http.ResponseWriter, r *http.Request) {
	st := s.allianceStore()
	if st == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	items, err := st.ListAllianceSystems(r.URL.Query().Get("q"), r.URL.Query().Get("platform"), r.URL.Query().Get("tag"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"systems": items})
}

func (s *Server) createAllianceSystem(w http.ResponseWriter, r *http.Request) {
	st := s.allianceStore()
	if st == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	var in db.AllianceSystemInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	in, err := db.NormalizeAllianceSystemInput(in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	id, err := st.CreateAllianceSystem(in)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (s *Server) getAllianceSystem(w http.ResponseWriter, r *http.Request) {
	st := s.allianceStore()
	if st == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	id, err := alliancePathID(r, "id")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	sys, eps, err := st.GetAllianceSystem(id)
	if err != nil {
		writeErr(w, 404, "联盟系统不存在")
		return
	}
	writeJSON(w, 200, map[string]any{"system": sys, "endpoints": eps})
}

func (s *Server) updateAllianceSystem(w http.ResponseWriter, r *http.Request) {
	st := s.allianceStore()
	if st == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	id, err := alliancePathID(r, "id")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var in db.AllianceSystemInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	in, err = db.NormalizeAllianceSystemInput(in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := st.UpdateAllianceSystem(id, in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) deleteAllianceSystem(w http.ResponseWriter, r *http.Request) {
	st := s.allianceStore()
	if st == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	id, err := alliancePathID(r, "id")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := st.DeleteAllianceSystem(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) addAllianceEndpoint(w http.ResponseWriter, r *http.Request) {
	st := s.allianceStore()
	if st == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	id, err := alliancePathID(r, "id")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var in db.AllianceEndpointInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	endpointID, err := st.AddAllianceEndpoint(id, in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": endpointID})
}

func (s *Server) deleteAllianceEndpoint(w http.ResponseWriter, r *http.Request) {
	st := s.allianceStore()
	if st == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	id, err := alliancePathID(r, "id")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	endpointID, err := alliancePathID(r, "endpointID")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := st.DeleteAllianceEndpoint(id, endpointID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) allianceStats(w http.ResponseWriter, r *http.Request) {
	st := s.allianceStore()
	if st == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	stats, err := st.AllianceStats()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, stats)
}

// allianceBriefPreview returns the exact briefing text task creation would
// append, so the picker can show it before submitting.
func (s *Server) allianceBriefPreview(w http.ResponseWriter, r *http.Request) {
	st := s.allianceStore()
	if st == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	ids := parseAllianceIDList(r.URL.Query().Get("ids"))
	systems, eps, err := s.loadAllianceTargets(st, ids)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"brief": buildAllianceBrief(systems, eps)})
}

// parseAllianceIDList parses "1,2,3" (duplicates dropped, order kept).
func parseAllianceIDList(raw string) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, part := range strings.Split(raw, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func (s *Server) loadAllianceTargets(st *db.AllianceStore, ids []int64) ([]*db.AllianceSystem, []*db.AllianceEndpoint, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	systems, err := st.ListAllianceSystemsByIDs(ids)
	if err != nil {
		return nil, nil, err
	}
	if len(systems) != len(ids) {
		return nil, nil, fmt.Errorf("联盟系统 id 无效或不存在")
	}
	eps, err := st.ListAllianceEndpointsForSystems(ids)
	if err != nil {
		return nil, nil, err
	}
	return systems, eps, nil
}

// resolveAllianceTargets registers each selected system's endpoints as standard
// assets (reusing existing rows by URL), returns their asset ids plus the
// bounded description briefing. Registering with taskID=0 keeps it side-effect
// free; the task creation transaction then attaches and approves the assets.
func (s *Server) resolveAllianceTargets(ids []int64) (assetIDs []int64, brief string, err error) {
	st := s.allianceStore()
	if st == nil {
		return nil, "", fmt.Errorf("database unavailable")
	}
	systems, eps, err := s.loadAllianceTargets(st, ids)
	if err != nil {
		return nil, "", err
	}
	as := s.assetStore()
	if as == nil {
		return nil, "", fmt.Errorf("database unavailable")
	}
	seen := map[int64]bool{}
	for _, ep := range eps {
		assetID := int64(0)
		if ep.AssetID != nil && *ep.AssetID > 0 {
			assetID = *ep.AssetID
		} else {
			id, regErr := as.RegisterUserAssetWithSource(0, "manual", "联盟系统台账登记", func(scoped *db.AssetStore) (int64, error) {
				return scoped.UpsertHTTPService(db.UpsertHTTPServiceReq{URL: ep.URL})
			})
			if regErr != nil {
				return nil, "", fmt.Errorf("入口 %s 登记资产失败：%w", ep.URL, regErr)
			}
			assetID = id
			if setErr := st.SetAllianceEndpointAsset(ep.ID, id); setErr != nil {
				log.Printf("[alliance] 记录入口 #%d 的资产失败: %v", ep.ID, setErr)
			}
		}
		if assetID > 0 && !seen[assetID] {
			seen[assetID] = true
			assetIDs = append(assetIDs, assetID)
		}
	}
	return assetIDs, buildAllianceBrief(systems, eps), nil
}

const (
	allianceBriefMaxSystems   = 10
	allianceBriefMaxRunes     = 2400
	allianceBriefFieldRunes   = 100 // 框架/业务/登录：一句话摘要（实测单系统约 50–80 字）
	allianceBriefNoteRunes    = 220 // 其他补充：测试账号、环境约束等必需信息
	allianceBriefMwRunes      = 100 // 中间件/组件
	allianceBriefHeaderPrefix = "【联盟画像】"
)

// buildAllianceBrief renders the bounded briefing appended to the task
// description. 任务描述会进探索图起点并被 planner 读取，必须短：最多 10 个
// 系统、总长 ≤1500 字，字段超长截断，其余系统只报数量。
func buildAllianceBrief(systems []*db.AllianceSystem, eps []*db.AllianceEndpoint) string {
	if len(systems) == 0 {
		return ""
	}
	byID := map[int64][]*db.AllianceEndpoint{}
	for _, ep := range eps {
		byID[ep.SystemID] = append(byID[ep.SystemID], ep)
	}
	var b strings.Builder
	header := allianceBriefHeaderPrefix + "来自联盟画像，测试时可参考以下背景（完整信息见「联盟画像」页）：\n"
	b.WriteString(header)
	used := len([]rune(header))
	written := 0
	for _, sys := range systems {
		if written >= allianceBriefMaxSystems {
			break
		}
		line := allianceBriefLine(written+1, sys, byID[sys.ID])
		if used+len([]rune(line))+1 > allianceBriefMaxRunes {
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
		used += len([]rune(line)) + 1
		written++
	}
	if rest := len(systems) - written; rest > 0 {
		fmt.Fprintf(&b, "（另有 %d 个选中系统未展开，可在任务内查看台账）\n", rest)
	}
	return strings.TrimRight(b.String(), "\n")
}

func allianceBriefLine(n int, sys *db.AllianceSystem, eps []*db.AllianceEndpoint) string {
	mainURL := sys.URL
	if mainURL == "" && len(eps) > 0 {
		mainURL = eps[0].URL
	}
	middleware, rest := allianceSplitSecurityNote(sys.SecurityNote)
	parts := []string{fmt.Sprintf("%d) %s", n, sys.Name)}
	if platform := allianceBriefClip(sys.Platform, allianceBriefFieldRunes); platform != "" {
		parts = append(parts, "["+platform+"]")
	}
	if mainURL != "" {
		parts = append(parts, mainURL)
	}
	if v := allianceBriefClip(sys.Framework, allianceBriefFieldRunes); v != "" {
		parts = append(parts, "框架："+v)
	}
	if v := allianceBriefClip(sys.Business, allianceBriefFieldRunes); v != "" {
		parts = append(parts, "业务："+v)
	}
	if v := allianceBriefClip(sys.Crypto, allianceBriefFieldRunes); v != "" {
		parts = append(parts, "登录/加解密："+v)
	}
	if v := allianceBriefClip(middleware, allianceBriefMwRunes); v != "" {
		parts = append(parts, "中间件："+v)
	}
	if v := allianceBriefClip(rest, allianceBriefNoteRunes); v != "" {
		parts = append(parts, "补充："+v)
	}
	return strings.Join(parts, "；")
}

// allianceSplitSecurityNote 拆开「中间件/组件：…」首行和其余补充内容。
// 台账表单把中间件写在补充的第一行，中间件单独占位后，补充里的测试账号等
// 必需信息才不会被中间件行吃掉全部预算。
func allianceSplitSecurityNote(note string) (middleware, rest string) {
	lines := strings.Split(strings.TrimSpace(note), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if middleware == "" {
			if v, ok := strings.CutPrefix(line, "中间件/组件："); ok {
				middleware = strings.TrimSpace(v)
				continue
			}
			if v, ok := strings.CutPrefix(line, "中间件组件："); ok {
				middleware = strings.TrimSpace(v)
				continue
			}
		}
		out = append(out, line)
	}
	return middleware, strings.Join(out, "；")
}

// allianceBriefClip 把任意字段压成单行，超长时按标点回退截断，避免把账号、
// URL、包名从中间切断（原来硬切 60 字会把「测试账号 13800000000」切成
// 「测试账号 1…」）。
func allianceBriefClip(raw string, max int) string {
	raw = strings.ReplaceAll(raw, "\r", "")
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\n", "；"))
	if raw == "" {
		return ""
	}
	runes := []rune(raw)
	if len(runes) <= max {
		return raw
	}
	cut := runes[:max]
	for i := len(cut) - 1; i >= max/2; i-- {
		switch cut[i] {
		case '；', ';', '，', ',', '。', '.', '、', ' ', '/', '（', '(':
			if trimmed := strings.TrimRight(string(cut[:i]), "；;，,、 ）)"); trimmed != "" {
				return trimmed + "…"
			}
		}
	}
	return string(cut) + "…"
}
