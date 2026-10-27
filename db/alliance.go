package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// =====================================================================
// 联盟系统（城商行联盟定制，feat/alliance-system，不合并上游）
//
// 人工维护的"作战情报台账"：一条 alliance_systems 描述一个业务系统
// （名称/入口/框架/加解密/业务/其他补充），入口拆到 alliance_endpoints；
// 新建任务时可选择系统，把入口登记成标准资产并把简报写进任务描述。
// 全部为加法表，与上游代码零耦合。
// =====================================================================

// AllianceSystem is one intel entry for a business system.
type AllianceSystem struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	OwnerUnit    string   `json:"owner_unit"`
	Platform     string   `json:"platform"`
	Framework    string   `json:"framework"`
	Crypto       string   `json:"crypto"`
	Business     string   `json:"business"`
	SecurityNote string   `json:"security_note"`
	Tags         []string `json:"tags"`
	Status       string   `json:"status"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	// EndpointCount is derived in list queries.
	EndpointCount int `json:"endpoint_count"`
}

// AllianceEndpoint is one entry point of a system. AssetID is filled when the
// endpoint has been registered as a standard asset (task creation or manual).
type AllianceEndpoint struct {
	ID       int64  `json:"id"`
	SystemID int64  `json:"system_id"`
	URL      string `json:"url"`
	Host     string `json:"host"`
	Port     *int   `json:"port,omitempty"`
	Scheme   string `json:"scheme"`
	Note     string `json:"note"`
	AssetID  *int64 `json:"asset_id,omitempty"`
}

// AllianceSystemInput is the create/update payload for a system.
type AllianceSystemInput struct {
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	OwnerUnit    string   `json:"owner_unit"`
	Platform     string   `json:"platform"`
	Framework    string   `json:"framework"`
	Crypto       string   `json:"crypto"`
	Business     string   `json:"business"`
	SecurityNote string   `json:"security_note"`
	Tags         []string `json:"tags"`
	Status       string   `json:"status"`
}

// AllianceEndpointInput is the create payload for an endpoint.
type AllianceEndpointInput struct {
	URL  string `json:"url"`
	Note string `json:"note"`
}

// AllianceBucket is one distribution row ("web × 12").
type AllianceBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// AllianceStats aggregates the台账 for the statistics tab.
type AllianceStats struct {
	Systems   int `json:"systems"`
	Endpoints int `json:"endpoints"`
	Covered   int `json:"covered"` // systems linked to at least one task
	Tasks     int `json:"tasks"`   // distinct linked tasks
	Findings  struct {
		Critical int `json:"critical"`
		High     int `json:"high"`
		Medium   int `json:"medium"`
		Low      int `json:"low"`
	} `json:"findings"`
	Platforms  []AllianceBucket `json:"platforms"`
	Frameworks []AllianceBucket `json:"frameworks"`
	Cryptos    []AllianceBucket `json:"cryptos"`
	Tags       []AllianceBucket `json:"tags"`
}

// AllianceStore operates on the three alliance tables.
type AllianceStore struct{ db *DB }

// Alliance returns the alliance store.
func (d *DB) Alliance() *AllianceStore { return &AllianceStore{db: d} }

const (
	maxAllianceTags     = 20
	maxAllianceTagRunes = 40
)

// NormalizeAllianceSystemInput trims and bounds the editable fields. Platform
// is free text on purpose (web/小程序/APP 混合的条目都存在)；status 是白名单，
// 列表页据此隐藏归档行。
func NormalizeAllianceSystemInput(in AllianceSystemInput) (AllianceSystemInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.URL = strings.TrimSpace(in.URL)
	in.OwnerUnit = strings.TrimSpace(in.OwnerUnit)
	in.Platform = strings.TrimSpace(in.Platform)
	in.Framework = strings.TrimSpace(in.Framework)
	in.Crypto = strings.TrimSpace(in.Crypto)
	in.Business = strings.TrimSpace(in.Business)
	in.SecurityNote = strings.TrimSpace(in.SecurityNote)
	if in.Name == "" {
		return in, fmt.Errorf("系统名称不能为空")
	}
	if len([]rune(in.Name)) > 120 {
		return in, fmt.Errorf("系统名称过长")
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Status != "active" && in.Status != "archived" {
		return in, fmt.Errorf("状态只能是 active / archived")
	}
	seen := map[string]bool{}
	tags := make([]string, 0, len(in.Tags))
	for _, tag := range in.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		if len([]rune(tag)) > maxAllianceTagRunes {
			return in, fmt.Errorf("标签 %q 过长（最多 %d 字）", tag, maxAllianceTagRunes)
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	if len(tags) > maxAllianceTags {
		return in, fmt.Errorf("标签最多 %d 个", maxAllianceTags)
	}
	in.Tags = tags
	return in, nil
}

// ParseAllianceEndpointURL normalizes an endpoint URL and derives host/port/scheme.
// A bare host is accepted and read as http://.
func ParseAllianceEndpointURL(raw string) (norm, host, scheme string, port *int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", nil, fmt.Errorf("入口 URL 不能为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", "", "", nil, fmt.Errorf("入口 URL 无效: %s", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", "", nil, fmt.Errorf("入口 URL 只支持 http/https: %s", raw)
	}
	host = u.Hostname()
	scheme = u.Scheme
	if p := u.Port(); p != "" {
		n := 0
		if _, err := fmt.Sscanf(p, "%d", &n); err != nil || n <= 0 || n > 65535 {
			return "", "", "", nil, fmt.Errorf("入口 URL 端口无效: %s", raw)
		}
		port = &n
	}
	return raw, host, scheme, port, nil
}

const allianceSystemCols = `id, name, COALESCE(url,''), COALESCE(owner_unit,''), COALESCE(platform,''),
COALESCE(framework,''), COALESCE(crypto,''), COALESCE(business,''), COALESCE(security_note,''),
COALESCE(array_to_json(tags), '[]'::json)::text, COALESCE(status,'active'), created_at::text, updated_at::text`

func scanAllianceSystem(sc interface{ Scan(...any) error }, withCount bool) (*AllianceSystem, error) {
	s := &AllianceSystem{}
	var tagsJSON string
	dest := []any{&s.ID, &s.Name, &s.URL, &s.OwnerUnit, &s.Platform, &s.Framework,
		&s.Crypto, &s.Business, &s.SecurityNote, &tagsJSON, &s.Status, &s.CreatedAt, &s.UpdatedAt}
	if withCount {
		dest = append(dest, &s.EndpointCount)
	}
	if err := sc.Scan(dest...); err != nil {
		return nil, err
	}
	if tagsJSON != "" {
		if err := json.Unmarshal([]byte(tagsJSON), &s.Tags); err != nil {
			return nil, fmt.Errorf("联盟系统 #%d 标签解析失败: %w", s.ID, err)
		}
	}
	if s.Tags == nil {
		s.Tags = []string{}
	}
	return s, nil
}

// ListAllianceSystems returns systems with endpoint counts, filtered by a
// free-text query (name/url/框架/业务/补充/单位), platform and tag.
func (s *AllianceStore) ListAllianceSystems(q, platform, tag string) ([]*AllianceSystem, error) {
	q = strings.TrimSpace(q)
	platform = strings.TrimSpace(platform)
	tag = strings.TrimSpace(tag)
	rows, err := s.db.Query(`
SELECT `+allianceSystemCols+`,
       (SELECT count(*) FROM alliance_endpoints e WHERE e.system_id = sys.id) AS endpoint_count
FROM alliance_systems sys
WHERE ($1 = '' OR sys.name ILIKE '%'||$1||'%' OR COALESCE(sys.url,'') ILIKE '%'||$1||'%'
       OR COALESCE(sys.framework,'') ILIKE '%'||$1||'%' OR COALESCE(sys.business,'') ILIKE '%'||$1||'%'
       OR COALESCE(sys.security_note,'') ILIKE '%'||$1||'%' OR COALESCE(sys.owner_unit,'') ILIKE '%'||$1||'%')
  AND ($2 = '' OR COALESCE(sys.platform,'') ILIKE '%'||$2||'%')
  AND ($3 = '' OR COALESCE(sys.tags,'{}')::text[] @> ARRAY[$3]::text[])
ORDER BY sys.updated_at DESC, sys.id DESC`, q, platform, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AllianceSystem
	for rows.Next() {
		sys, err := scanAllianceSystem(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, sys)
	}
	return out, rows.Err()
}

// ListAllianceSystemsByIDs loads systems (no endpoint counts) for task creation.
func (s *AllianceStore) ListAllianceSystemsByIDs(ids []int64) ([]*AllianceSystem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT `+allianceSystemCols+` FROM alliance_systems WHERE id=ANY($1::bigint[]) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AllianceSystem
	for rows.Next() {
		sys, err := scanAllianceSystem(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, sys)
	}
	return out, rows.Err()
}

// GetAllianceSystem returns one system plus its endpoints.
func (s *AllianceStore) GetAllianceSystem(id int64) (*AllianceSystem, []*AllianceEndpoint, error) {
	sys, err := scanAllianceSystem(s.db.QueryRow(`SELECT `+allianceSystemCols+` FROM alliance_systems WHERE id=$1`, id), false)
	if err != nil {
		return nil, nil, err
	}
	eps, err := s.ListAllianceEndpoints(id)
	if err != nil {
		return nil, nil, err
	}
	return sys, eps, nil
}

// CreateAllianceSystem inserts a system and, when a main URL is given, its
// first endpoint. Runs in one transaction.
func (s *AllianceStore) CreateAllianceSystem(in AllianceSystemInput) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	var id int64
	if err := tx.QueryRow(`
INSERT INTO alliance_systems(name, url, owner_unit, platform, framework, crypto, business, security_note, tags, status)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::text[],$10) RETURNING id`,
		in.Name, in.URL, in.OwnerUnit, in.Platform, in.Framework, in.Crypto, in.Business, in.SecurityNote, marshalStringArray(in.Tags), in.Status).Scan(&id); err != nil {
		return 0, err
	}
	if in.URL != "" {
		if _, err := insertAllianceEndpointTx(tx, id, in.URL, ""); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// UpdateAllianceSystem overwrites the editable fields; a new main URL that is not
// yet an endpoint gets added so task selection can resolve it.
func (s *AllianceStore) UpdateAllianceSystem(id int64, in AllianceSystemInput) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.Exec(`
UPDATE alliance_systems
SET name=$2, url=$3, owner_unit=$4, platform=$5, framework=$6, crypto=$7,
    business=$8, security_note=$9, tags=$10::text[], status=$11, updated_at=now()
WHERE id=$1`,
		id, in.Name, in.URL, in.OwnerUnit, in.Platform, in.Framework, in.Crypto, in.Business, in.SecurityNote, marshalStringArray(in.Tags), in.Status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("联盟系统 #%d 不存在", id)
	}
	if in.URL != "" {
		if _, err := insertAllianceEndpointTx(tx, id, in.URL, ""); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteAllianceSystem removes a system (endpoints and links cascade).
func (s *AllianceStore) DeleteAllianceSystem(id int64) error {
	_, err := s.db.Exec(`DELETE FROM alliance_systems WHERE id=$1`, id)
	return err
}

// ListAllianceEndpoints returns the endpoints of one system.
func (s *AllianceStore) ListAllianceEndpoints(systemID int64) ([]*AllianceEndpoint, error) {
	return s.listAllianceEndpoints(`WHERE system_id=$1`, systemID)
}

// ListAllianceEndpointsForSystems returns the endpoints of several systems
// (task creation resolves these into standard assets).
func (s *AllianceStore) ListAllianceEndpointsForSystems(systemIDs []int64) ([]*AllianceEndpoint, error) {
	if len(systemIDs) == 0 {
		return nil, nil
	}
	return s.listAllianceEndpoints(`WHERE system_id=ANY($1::bigint[])`, systemIDs)
}

func (s *AllianceStore) listAllianceEndpoints(where string, arg any) ([]*AllianceEndpoint, error) {
	rows, err := s.db.Query(`SELECT id, system_id, COALESCE(url,''), COALESCE(host,''), port,
COALESCE(scheme,''), COALESCE(note,''), asset_id
FROM alliance_endpoints `+where+` ORDER BY id`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AllianceEndpoint
	for rows.Next() {
		e := &AllianceEndpoint{}
		if err := rows.Scan(&e.ID, &e.SystemID, &e.URL, &e.Host, &e.Port, &e.Scheme, &e.Note, &e.AssetID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// insertAllianceEndpointTx upserts one endpoint inside a transaction (the
// system's main URL is stored here so task selection can resolve it).
func insertAllianceEndpointTx(tx *sql.Tx, systemID int64, rawURL, note string) (int64, error) {
	norm, host, scheme, port, err := ParseAllianceEndpointURL(rawURL)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(`
INSERT INTO alliance_endpoints(system_id, url, host, port, scheme, note)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (system_id, url) DO UPDATE SET note=EXCLUDED.note
RETURNING id`, systemID, norm, host, port, scheme, note).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// AddAllianceEndpoint adds one extra entry point (deduped by system+url).
func (s *AllianceStore) AddAllianceEndpoint(systemID int64, in AllianceEndpointInput) (int64, error) {
	norm, host, scheme, port, err := ParseAllianceEndpointURL(in.URL)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := s.db.QueryRow(`
INSERT INTO alliance_endpoints(system_id, url, host, port, scheme, note)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (system_id, url) DO UPDATE SET note=EXCLUDED.note
RETURNING id`, systemID, norm, host, port, scheme, strings.TrimSpace(in.Note)).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// DeleteAllianceEndpoint removes one endpoint from a system.
func (s *AllianceStore) DeleteAllianceEndpoint(systemID, endpointID int64) error {
	_, err := s.db.Exec(`DELETE FROM alliance_endpoints WHERE id=$1 AND system_id=$2`, endpointID, systemID)
	return err
}

// SetAllianceEndpointAsset remembers which standard asset an endpoint resolved to.
func (s *AllianceStore) SetAllianceEndpointAsset(endpointID, assetID int64) error {
	_, err := s.db.Exec(`UPDATE alliance_endpoints SET asset_id=$2 WHERE id=$1`, endpointID, assetID)
	return err
}

// LinkAllianceSystemsToTask records which systems a task targets (statistics).
func (s *AllianceStore) LinkAllianceSystemsToTask(taskID int64, systemIDs []int64) error {
	if taskID <= 0 || len(systemIDs) == 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO alliance_task_links(task_id, system_id)
SELECT $1, id FROM alliance_systems WHERE id=ANY($2::bigint[])
ON CONFLICT (task_id, system_id) DO NOTHING`, taskID, systemIDs)
	return err
}

// AllianceStats aggregates totals, distributions and task/finding coverage.
func (s *AllianceStore) AllianceStats() (*AllianceStats, error) {
	out := &AllianceStats{}
	if err := s.db.QueryRow(`SELECT
  (SELECT count(*) FROM alliance_systems),
  (SELECT count(*) FROM alliance_endpoints),
  (SELECT count(DISTINCT system_id) FROM alliance_task_links),
  (SELECT count(DISTINCT task_id) FROM alliance_task_links)`).
		Scan(&out.Systems, &out.Endpoints, &out.Covered, &out.Tasks); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT
  count(*) FILTER (WHERE f.severity='critical'),
  count(*) FILTER (WHERE f.severity='high'),
  count(*) FILTER (WHERE f.severity='medium'),
  count(*) FILTER (WHERE f.severity='low')
FROM findings f JOIN alliance_task_links l ON l.task_id=f.task_id`).
		Scan(&out.Findings.Critical, &out.Findings.High, &out.Findings.Medium, &out.Findings.Low); err != nil {
		return nil, err
	}
	var err error
	if out.Platforms, err = s.allianceBuckets(`SELECT COALESCE(NULLIF(trim(platform),''),'(未填)') AS label, count(*) FROM alliance_systems GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 12`); err != nil {
		return nil, err
	}
	if out.Frameworks, err = s.allianceBuckets(`SELECT COALESCE(NULLIF(trim(framework),''),'(未填)') AS label, count(*) FROM alliance_systems GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 12`); err != nil {
		return nil, err
	}
	if out.Cryptos, err = s.allianceBuckets(`SELECT COALESCE(NULLIF(trim(crypto),''),'(未填)') AS label, count(*) FROM alliance_systems GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 12`); err != nil {
		return nil, err
	}
	if out.Tags, err = s.allianceBuckets(`SELECT tag AS label, count(*) FROM alliance_systems, unnest(COALESCE(tags,'{}')::text[]) AS tag GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 20`); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *AllianceStore) allianceBuckets(q string) ([]AllianceBucket, error) {
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AllianceBucket
	for rows.Next() {
		var b AllianceBucket
		if err := rows.Scan(&b.Label, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
