package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neouks/series/allianceprofile"
)

// =====================================================================
// 联盟画像存储层（feat/alliance-profile）
//
// 设计要点：
//   - alliance_systems.document 是画像内容事实源；扁平列是索引/统计投影，
//     保存时同步，旧 PATCH 入口也会把列值合并回文档，避免两份内容分叉。
//   - 任务快照按 (task_id, system_id) 存，不设 system 外键：删除系统后历史任务
//     仍能解释“当时依据什么测试”。
//   - 反哺只读本任务已确认的事实/发现，建议必须人工审核后才写正式画像。
// =====================================================================

var (
	// ErrAllianceProfileConflict 版本不匹配 / 建议已被相反决定处理。
	ErrAllianceProfileConflict = errors.New("联盟画像版本冲突")
	// ErrAllianceProfileArchived 归档画像不能修改或接受建议。
	ErrAllianceProfileArchived = errors.New("联盟画像已归档")
	// ErrAllianceProfileNotFound 画像/建议不存在。
	ErrAllianceProfileNotFound = errors.New("联盟画像不存在")
	// ErrAllianceProfileInvalid 文档或建议内容无效。
	ErrAllianceProfileInvalid = errors.New("联盟画像内容无效")
)

// AllianceSystemProfile is the portrait projection of one system.
type AllianceSystemProfile struct {
	SystemID    int64           `json:"system_id"`
	Name        string          `json:"name"`
	Environment string          `json:"environment"`
	Version     int64           `json:"version"`
	Document    json.RawMessage `json:"document"`
}

// AllianceRevision is one historical version.
type AllianceRevision struct {
	Version   int64  `json:"version"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
}

// TaskAllianceSnapshot is the frozen portrait bound to a task.
// Snapshot is json.RawMessage, not []byte: []byte marshals as a base64/escaped
// string, while the frontend contract expects the snapshot document object.
type TaskAllianceSnapshot struct {
	SystemID       int64           `json:"system_id"`
	SystemName     string          `json:"system_name"`
	ProfileVersion int64           `json:"profile_version"`
	Snapshot       json.RawMessage `json:"snapshot"`
}

// AllianceTaskReading is the soft reading state for one task.
type AllianceTaskReading struct {
	TaskID int64  `json:"task_id"`
	State  string `json:"state"`
	Result string `json:"result"`
	Error  string `json:"error"`
	ReadAt string `json:"read_at,omitempty"`
}

// AllianceSuggestionInput is the model-produced candidate (also stored inside
// alliance_profile_suggestions.suggestion).
type AllianceSuggestionInput struct {
	SystemID      int64   `json:"system_id"`
	SectionID     string  `json:"section_id"`
	SectionTitle  string  `json:"section_title"`
	Phase         int     `json:"phase"`
	FieldID       string  `json:"field_id"`
	FieldTitle    string  `json:"field_title"`
	ProposedValue string  `json:"proposed_value"`
	Reason        string  `json:"reason"`
	EvidenceIDs   []int64 `json:"evidence_ids"`
}

// AllianceSuggestion is one reviewable candidate.
type AllianceSuggestion struct {
	ID              int64                   `json:"id"`
	RunID           int64                   `json:"run_id"`
	TaskID          int64                   `json:"task_id"`
	SystemID        int64                   `json:"system_id"`
	Suggestion      AllianceSuggestionInput `json:"suggestion"`
	State           string                  `json:"state"`
	AcceptedValue   string                  `json:"accepted_value"`
	AcceptedVersion *int64                  `json:"accepted_version,omitempty"`
	ReviewedAt      string                  `json:"reviewed_at,omitempty"`
	CreatedAt       string                  `json:"created_at"`
}

// AllianceFeedbackRun tracks one post-task suggestion run.
type AllianceFeedbackRun struct {
	ID          int64  `json:"id"`
	TaskID      int64  `json:"task_id"`
	CompletedAt string `json:"completed_at"`
	State       string `json:"state"`
	Error       string `json:"error"`
	Attempt     int64  `json:"attempt"`
	UpdatedAt   string `json:"updated_at"`
}

// AllianceEvidence is one confirmed fact/finding node.
type AllianceEvidence struct {
	ID      int64           `json:"id"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// allianceSnapshotJSON builds the frozen JSON stored in task snapshots and
// revisions: {id,name,environment,version,document}.
func allianceSnapshotJSON(id int64, name, environment string, version int64, doc json.RawMessage) ([]byte, error) {
	var d allianceprofile.Document
	if len(doc) == 0 || strings.TrimSpace(string(doc)) == "{}" {
		d = allianceprofile.Template()
	} else if err := json.Unmarshal(doc, &d); err != nil {
		return nil, fmt.Errorf("%w: 画像文档解析失败: %v", ErrAllianceProfileInvalid, err)
	}
	return json.Marshal(allianceprofile.Snapshot{
		ID: id, Name: name, Environment: environment, Version: version, Document: d,
	})
}

// allianceDefaultDocument builds the first portrait from the legacy columns.
func allianceDefaultDocument(in AllianceSystemInput) ([]byte, error) {
	d := allianceprofile.BuildDefault(allianceprofile.LegacyFields{
		Name: in.Name, Environment: in.Environment, OwnerUnit: in.OwnerUnit,
		Framework: in.Framework, Crypto: in.Crypto, Business: in.Business,
		SecurityNote: in.SecurityNote,
	})
	if err := d.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAllianceProfileInvalid, err)
	}
	return json.Marshal(d)
}

// allianceMergeLegacyDocument keeps an existing document authoritative.
//
// The old PATCH endpoint used to copy legacy flat columns into mapped portrait
// fields. Those fields are not semantically equivalent (S1-03 is contacts, not
// owner unit; S1-04 is business + first-round goal, not business alone), so the
// copy polluted the ledger with long profile text. New rows still get a default
// document from allianceDefaultDocument; existing documents are left untouched.
func allianceMergeLegacyDocument(oldDoc string, in AllianceSystemInput) ([]byte, bool, error) {
	if strings.TrimSpace(oldDoc) == "" || strings.TrimSpace(oldDoc) == "{}" {
		raw, err := allianceDefaultDocument(in)
		return raw, true, err
	}
	var d allianceprofile.Document
	if err := json.Unmarshal([]byte(oldDoc), &d); err != nil {
		return nil, false, fmt.Errorf("%w: 画像文档解析失败: %v", ErrAllianceProfileInvalid, err)
	}
	if err := d.Validate(); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrAllianceProfileInvalid, err)
	}
	return []byte(oldDoc), false, nil
}

// insertAllianceRevisionTx writes one revision row. Snapshot is the same shape
// as a task snapshot so history and task binding are directly comparable.
func insertAllianceRevisionTx(tx *sql.Tx, id, version int64, name, environment, doc, source string) error {
	snap, err := allianceSnapshotJSON(id, name, environment, version, json.RawMessage(doc))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO alliance_profile_revisions(system_id, version, snapshot, source)
VALUES ($1,$2,$3::jsonb,$4) ON CONFLICT (system_id, version) DO NOTHING`, id, version, string(snap), source)
	return err
}

// BackfillAllianceProfiles fills documents for rows created before the portrait
// release. Idempotent: only rows with document='{}' are touched.
func (s *AllianceStore) BackfillAllianceProfiles() error {
	rows, err := s.db.Query(`SELECT id, name, COALESCE(environment,''), COALESCE(owner_unit,''),
COALESCE(framework,''), COALESCE(crypto,''), COALESCE(business,''), COALESCE(security_note,''),
COALESCE(version,1)
FROM alliance_systems WHERE document IS NULL OR document='{}'::jsonb ORDER BY id`)
	if err != nil {
		return err
	}
	type legacyRow struct {
		id                                                    int64
		name, environment, owner, framework, crypto, business string
		securityNote                                          string
		version                                               int64
	}
	var pending []legacyRow
	for rows.Next() {
		var r legacyRow
		if err := rows.Scan(&r.id, &r.name, &r.environment, &r.owner, &r.framework, &r.crypto, &r.business, &r.securityNote, &r.version); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range pending {
		d := allianceprofile.BuildDefault(allianceprofile.LegacyFields{
			Name: r.name, Environment: r.environment, OwnerUnit: r.owner,
			Framework: r.framework, Crypto: r.crypto, Business: r.business,
			SecurityNote: r.securityNote,
		})
		if err := d.Validate(); err != nil {
			return fmt.Errorf("联盟系统 #%d 画像回填失败: %w", r.id, err)
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		version := r.version
		if version <= 0 {
			version = 1
		}
		if _, err := tx.Exec(`UPDATE alliance_systems SET document=$2::jsonb, version=$3 WHERE id=$1 AND document='{}'::jsonb`,
			r.id, string(raw), version); err != nil {
			tx.Rollback()
			return err
		}
		if err := insertAllianceRevisionTx(tx, r.id, version, r.name, r.environment, string(raw), "legacy-migration"); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// GetAllianceProfile loads the portrait body of one system.
func (s *AllianceStore) GetAllianceProfile(id int64) (*AllianceSystemProfile, error) {
	var p AllianceSystemProfile
	var doc string
	err := s.db.QueryRow(`SELECT id, name, COALESCE(environment,''), COALESCE(version,1), COALESCE(document::text,'{}')
FROM alliance_systems WHERE id=$1`, id).Scan(&p.SystemID, &p.Name, &p.Environment, &p.Version, &doc)
	if err == sql.ErrNoRows {
		return nil, ErrAllianceProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(doc) == "" || strings.TrimSpace(doc) == "{}" {
		// Lazy backfill for a row created after startup but before this release.
		var owner, framework, crypto, business, note string
		if err := s.db.QueryRow(`SELECT COALESCE(owner_unit,''), COALESCE(framework,''), COALESCE(crypto,''),
COALESCE(business,''), COALESCE(security_note,'') FROM alliance_systems WHERE id=$1`, id).
			Scan(&owner, &framework, &crypto, &business, &note); err != nil {
			return nil, err
		}
		d := allianceprofile.BuildDefault(allianceprofile.LegacyFields{
			Name: p.Name, Environment: p.Environment, OwnerUnit: owner,
			Framework: framework, Crypto: crypto, Business: business, SecurityNote: note,
		})
		raw, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		doc = string(raw)
	}
	p.Document = json.RawMessage(doc)
	return &p, nil
}

// SaveAllianceProfile replaces the document with optimistic concurrency. The
// flat columns are synchronised inside the same transaction.
func (s *AllianceStore) SaveAllianceProfile(id, expectedVersion int64, environment string, doc json.RawMessage) (*AllianceSystemProfile, error) {
	var d allianceprofile.Document
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAllianceProfileInvalid, err)
	}
	if err := d.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAllianceProfileInvalid, err)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var name, status string
	var version int64
	var currentDoc, currentEnv string
	if err := tx.QueryRow(`SELECT name, COALESCE(status,'active'), COALESCE(version,1),
COALESCE(document::text,'{}'), COALESCE(environment,'') FROM alliance_systems WHERE id=$1 FOR UPDATE`, id).
		Scan(&name, &status, &version, &currentDoc, &currentEnv); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrAllianceProfileNotFound
		}
		return nil, err
	}
	if status == "archived" {
		return nil, ErrAllianceProfileArchived
	}
	if expectedVersion > 0 && expectedVersion != version {
		return nil, ErrAllianceProfileConflict
	}
	// A no-op save (same document and environment) must not inflate versions.
	// Compare canonical JSON so whitespace / key order differences do not count.
	if strings.TrimSpace(currentDoc) != "" && strings.TrimSpace(currentDoc) != "{}" &&
		strings.TrimSpace(currentEnv) == strings.TrimSpace(environment) {
		var previous allianceprofile.Document
		if err := json.Unmarshal([]byte(currentDoc), &previous); err == nil {
			previousRaw, err := json.Marshal(previous)
			if err == nil && string(previousRaw) == string(raw) {
				if err := tx.Commit(); err != nil {
					return nil, err
				}
				return &AllianceSystemProfile{
					SystemID: id, Name: name, Environment: strings.TrimSpace(currentEnv),
					Version: version, Document: raw,
				}, nil
			}
		}
	}
	newVersion := version + 1
	if _, err := tx.Exec(`UPDATE alliance_systems SET document=$2::jsonb, version=$3, environment=$4, updated_at=now() WHERE id=$1`,
		id, string(raw), newVersion, strings.TrimSpace(environment)); err != nil {
		return nil, err
	}
	if err := insertAllianceRevisionTx(tx, id, newVersion, name, strings.TrimSpace(environment), string(raw), "manual"); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &AllianceSystemProfile{SystemID: id, Name: name, Environment: strings.TrimSpace(environment), Version: newVersion, Document: raw}, nil
}

// AllianceProfileHistory lists versions newest first.
func (s *AllianceStore) AllianceProfileHistory(id int64) ([]AllianceRevision, error) {
	rows, err := s.db.Query(`SELECT version, COALESCE(source,''), created_at::text
FROM alliance_profile_revisions WHERE system_id=$1 ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AllianceRevision
	for rows.Next() {
		var r AllianceRevision
		if err := rows.Scan(&r.Version, &r.Source, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BuildTaskSnapshots freezes the current portrait of each selected system.
func (s *AllianceStore) BuildTaskSnapshots(ids []int64) ([]TaskAllianceSnapshot, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id, name, COALESCE(environment,''), COALESCE(version,1),
COALESCE(owner_unit,''), COALESCE(framework,''), COALESCE(crypto,''), COALESCE(business,''),
COALESCE(security_note,''), COALESCE(document::text,'{}')
FROM alliance_systems WHERE id=ANY($1::bigint[]) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskAllianceSnapshot
	for rows.Next() {
		var id, version int64
		var name, env, owner, framework, crypto, business, note, doc string
		if err := rows.Scan(&id, &name, &env, &version, &owner, &framework, &crypto, &business, &note, &doc); err != nil {
			return nil, err
		}
		if strings.TrimSpace(doc) == "" || strings.TrimSpace(doc) == "{}" {
			d := allianceprofile.BuildDefault(allianceprofile.LegacyFields{
				Name: name, Environment: env, OwnerUnit: owner, Framework: framework,
				Crypto: crypto, Business: business, SecurityNote: note,
			})
			raw, err := json.Marshal(d)
			if err != nil {
				return nil, err
			}
			doc = string(raw)
		}
		snap, err := allianceSnapshotJSON(id, name, env, version, json.RawMessage(doc))
		if err != nil {
			return nil, err
		}
		out = append(out, TaskAllianceSnapshot{SystemID: id, SystemName: name, ProfileVersion: version, Snapshot: snap})
	}
	return out, rows.Err()
}

// insertAllianceTaskSnapshotsTx is called inside task creation.
func insertAllianceTaskSnapshotsTx(tx *sql.Tx, taskID int64, snaps []TaskAllianceSnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	for _, snap := range snaps {
		if _, err := tx.Exec(`INSERT INTO alliance_task_snapshots(task_id, system_id, system_name, profile_version, snapshot)
VALUES ($1,$2,$3,$4,$5::jsonb) ON CONFLICT (task_id, system_id) DO NOTHING`,
			taskID, snap.SystemID, snap.SystemName, snap.ProfileVersion, string(snap.Snapshot)); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO alliance_task_readings(task_id, state) VALUES ($1,'pending')
ON CONFLICT (task_id) DO NOTHING`, taskID)
	return err
}

// TaskAllianceSnapshots loads the frozen snapshots of one task.
func (s *AllianceStore) TaskAllianceSnapshots(taskID int64) ([]TaskAllianceSnapshot, error) {
	rows, err := s.db.Query(`SELECT system_id, system_name, profile_version, snapshot::text
FROM alliance_task_snapshots WHERE task_id=$1 ORDER BY system_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskAllianceSnapshot
	for rows.Next() {
		var snap TaskAllianceSnapshot
		var raw string
		if err := rows.Scan(&snap.SystemID, &snap.SystemName, &snap.ProfileVersion, &raw); err != nil {
			return nil, err
		}
		snap.Snapshot = json.RawMessage(raw)
		out = append(out, snap)
	}
	return out, rows.Err()
}

// TaskAllianceReading returns nil when the task never bound a portrait.
func (s *AllianceStore) TaskAllianceReading(taskID int64) (*AllianceTaskReading, error) {
	var r AllianceTaskReading
	var readAt sql.NullString
	err := s.db.QueryRow(`SELECT task_id, state, result, error, read_at::text FROM alliance_task_readings WHERE task_id=$1`, taskID).
		Scan(&r.TaskID, &r.State, &r.Result, &r.Error, &readAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if readAt.Valid {
		r.ReadAt = readAt.String
	}
	return &r, nil
}

// SetAllianceTaskReading updates the soft reading state.
func (s *AllianceStore) SetAllianceTaskReading(taskID int64, state, result, errText string) error {
	_, err := s.db.Exec(`INSERT INTO alliance_task_readings(task_id, state, result, error, read_at, updated_at)
VALUES ($1,$2,$3,$4, CASE WHEN $2='ready' THEN now() ELSE NULL END, now())
ON CONFLICT (task_id) DO UPDATE SET state=EXCLUDED.state, result=EXCLUDED.result,
error=EXCLUDED.error, read_at=EXCLUDED.read_at, updated_at=now()`, taskID, state, result, errText)
	return err
}

// AllianceProfileContext renders the bounded prompt block for a task.
func (d *DB) AllianceProfileContext(taskID int64) string {
	if d == nil || taskID <= 0 {
		return ""
	}
	st := d.Alliance()
	snaps, err := st.TaskAllianceSnapshots(taskID)
	if err != nil || len(snaps) == 0 {
		return ""
	}
	parsed := make([]allianceprofile.Snapshot, 0, len(snaps))
	for _, s := range snaps {
		var snap allianceprofile.Snapshot
		if err := json.Unmarshal(s.Snapshot, &snap); err != nil {
			continue
		}
		parsed = append(parsed, snap)
	}
	reading := ""
	if r, err := st.TaskAllianceReading(taskID); err == nil && r != nil {
		reading = r.Result
	}
	return allianceprofile.RenderContext(parsed, reading, allianceprofile.MaxInjectedRunes)
}

// AllianceProfileContext is the ExplorationStore wrapper used by agents.
func (s *ExplorationStore) AllianceProfileContext(taskID int64) string {
	if s == nil || s.db == nil {
		return ""
	}
	return s.db.AllianceProfileContext(taskID)
}

// ---- 反哺 runs / suggestions ----

// DiscoverAllianceFeedbackRuns creates one run per finished task that has a
// snapshot. Idempotent via UNIQUE(task_id, completed_at).
func (s *AllianceStore) DiscoverAllianceFeedbackRuns() error {
	_, err := s.db.Exec(`INSERT INTO alliance_profile_feedback_runs(task_id, completed_at)
SELECT t.id, t.completed_at FROM tasks t
JOIN alliance_task_snapshots s ON s.task_id=t.id
WHERE t.status IN ('done','failed','timeout') AND t.completed_at IS NOT NULL
  AND t.deleted_at IS NULL AND t.archived_at IS NULL
ON CONFLICT (task_id, completed_at) DO NOTHING`)
	return err
}

// DueAllianceFeedbackRuns returns pending runs and expired leases.
func (s *AllianceStore) DueAllianceFeedbackRuns() ([]*AllianceFeedbackRun, error) {
	rows, err := s.db.Query(`SELECT r.id, r.task_id, r.completed_at::text, r.state, r.error, r.attempt, r.updated_at::text
FROM alliance_profile_feedback_runs r JOIN tasks t ON t.id=r.task_id
WHERE (r.state='pending' OR (r.state='running' AND r.lease_until < now()))
  AND t.deleted_at IS NULL AND t.archived_at IS NULL
  AND t.status IN ('done','failed','timeout') AND t.completed_at=r.completed_at
ORDER BY r.id LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AllianceFeedbackRun
	for rows.Next() {
		r := &AllianceFeedbackRun{}
		if err := rows.Scan(&r.ID, &r.TaskID, &r.CompletedAt, &r.State, &r.Error, &r.Attempt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimAllianceFeedbackRun takes a 10-minute lease.
func (s *AllianceStore) ClaimAllianceFeedbackRun(id int64) (*AllianceFeedbackRun, error) {
	r := &AllianceFeedbackRun{}
	err := s.db.QueryRow(`UPDATE alliance_profile_feedback_runs
SET state='running', attempt=attempt+1, error='', lease_until=now()+interval '10 minutes', updated_at=now()
WHERE id=$1 AND (state='pending' OR (state='running' AND lease_until < now()))
RETURNING id, task_id, completed_at::text, state, error, attempt, updated_at::text`, id).
		Scan(&r.ID, &r.TaskID, &r.CompletedAt, &r.State, &r.Error, &r.Attempt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrAllianceProfileConflict
	}
	return r, err
}

// FailAllianceFeedbackRun marks a claimed run failed (attempt protects against
// overwriting a newer claim).
func (s *AllianceStore) FailAllianceFeedbackRun(id, attempt int64, detail string) error {
	_, err := s.db.Exec(`UPDATE alliance_profile_feedback_runs SET state='failed', error=$3, lease_until=NULL, updated_at=now()
WHERE id=$1 AND attempt=$2 AND state='running'`, id, attempt, detail)
	return err
}

// RetryAllianceFeedbackRun requeues one failed run.
func (s *AllianceStore) RetryAllianceFeedbackRun(taskID, runID int64) error {
	res, err := s.db.Exec(`UPDATE alliance_profile_feedback_runs r SET state='pending', error='', lease_until=NULL, updated_at=now()
FROM tasks t WHERE r.id=$1 AND r.task_id=$2 AND t.id=r.task_id AND t.deleted_at IS NULL
AND t.status IN ('done','failed','timeout') AND t.completed_at=r.completed_at AND r.state='failed'`, runID, taskID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrAllianceProfileConflict
	}
	return nil
}

// ProfileEvidencePage returns confirmed facts/findings of the task, oldest first.
func (s *AllianceStore) ProfileEvidencePage(taskID, afterID int64) ([]AllianceEvidence, error) {
	rows, err := s.db.Query(`SELECT n.id, n.kind, n.payload::text
FROM exploration_nodes n JOIN tasks t ON t.exploration_id=n.exploration_id
WHERE t.id=$1 AND n.id>$2 AND n.kind IN ('fact','finding') AND n.state='confirmed'
ORDER BY n.id LIMIT 64`, taskID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AllianceEvidence
	for rows.Next() {
		var e AllianceEvidence
		var payload string
		if err := rows.Scan(&e.ID, &e.Kind, &payload); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CompleteAllianceFeedbackRun validates and stores the candidates atomically.
func (s *AllianceStore) CompleteAllianceFeedbackRun(run *AllianceFeedbackRun, candidates []AllianceSuggestionInput) error {
	if len(candidates) > 200 {
		return fmt.Errorf("%w: 反馈候选超过 200 条", ErrAllianceProfileInvalid)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	// Only systems bound to this task may receive suggestions.
	allowed := map[int64]bool{}
	rows, err := tx.Query(`SELECT system_id FROM alliance_task_snapshots WHERE task_id=$1`, run.TaskID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		allowed[id] = true
	}
	rows.Close()
	for _, c := range candidates {
		if !allowed[c.SystemID] {
			return fmt.Errorf("%w: 建议引用了本任务之外的系统 #%d", ErrAllianceProfileInvalid, c.SystemID)
		}
		if strings.TrimSpace(c.FieldID) == "" || strings.TrimSpace(c.SectionID) == "" || strings.TrimSpace(c.ProposedValue) == "" {
			return fmt.Errorf("%w: 建议缺少字段或内容", ErrAllianceProfileInvalid)
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO alliance_profile_suggestions(run_id, task_id, system_id, suggestion)
VALUES ($1,$2,$3,$4::jsonb)`, run.ID, run.TaskID, c.SystemID, string(raw)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE alliance_profile_feedback_runs SET state='done', error='', lease_until=NULL, updated_at=now()
WHERE id=$1 AND state='running'`, run.ID); err != nil {
		return err
	}
	return tx.Commit()
}

const allianceSuggestionCols = `id, run_id, task_id, system_id, suggestion::text, state,
COALESCE(accepted_value,''), accepted_version, COALESCE(reviewed_at::text,''), created_at::text`

func scanAllianceSuggestion(row interface{ Scan(...any) error }) (*AllianceSuggestion, error) {
	s := &AllianceSuggestion{}
	var raw string
	var version sql.NullInt64
	if err := row.Scan(&s.ID, &s.RunID, &s.TaskID, &s.SystemID, &raw, &s.State, &s.AcceptedValue, &version, &s.ReviewedAt, &s.CreatedAt); err != nil {
		return nil, err
	}
	if version.Valid {
		v := version.Int64
		s.AcceptedVersion = &v
	}
	if err := json.Unmarshal([]byte(raw), &s.Suggestion); err != nil {
		return nil, err
	}
	return s, nil
}

// ListAllianceSuggestions filters by system and/or task (0 = any).
func (s *AllianceStore) ListAllianceSuggestions(systemID, taskID int64) ([]*AllianceSuggestion, error) {
	rows, err := s.db.Query(`SELECT `+allianceSuggestionCols+` FROM alliance_profile_suggestions
WHERE ($1=0 OR system_id=$1) AND ($2=0 OR task_id=$2) ORDER BY id DESC LIMIT 500`, systemID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AllianceSuggestion
	for rows.Next() {
		item, err := scanAllianceSuggestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// GetAllianceSuggestion loads one candidate.
func (s *AllianceStore) GetAllianceSuggestion(id int64) (*AllianceSuggestion, error) {
	item, err := scanAllianceSuggestion(s.db.QueryRow(`SELECT `+allianceSuggestionCols+` FROM alliance_profile_suggestions WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, ErrAllianceProfileNotFound
	}
	return item, err
}

// ReviewAllianceSuggestion accepts or rejects one candidate. Accepting applies
// the field to the live document and creates a new version in one transaction.
func (s *AllianceStore) ReviewAllianceSuggestion(id, expectedVersion int64, decision, value string) (*AllianceSuggestion, error) {
	if decision != "accepted" && decision != "rejected" {
		return nil, fmt.Errorf("%w: decision 只能是 accepted/rejected", ErrAllianceProfileInvalid)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	item, err := scanAllianceSuggestion(tx.QueryRow(`SELECT `+allianceSuggestionCols+` FROM alliance_profile_suggestions WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrAllianceProfileNotFound
		}
		return nil, err
	}
	if item.State != "pending" {
		if item.State == decision {
			return item, tx.Commit()
		}
		return nil, ErrAllianceProfileConflict
	}
	if decision == "accepted" {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%w: 确认内容不能为空", ErrAllianceProfileInvalid)
		}
		var name, status, doc, environment string
		var version int64
		if err := tx.QueryRow(`SELECT name, COALESCE(status,'active'), COALESCE(document::text,'{}'), COALESCE(version,1), COALESCE(environment,'')
FROM alliance_systems WHERE id=$1 FOR UPDATE`, item.SystemID).Scan(&name, &status, &doc, &version, &environment); err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrAllianceProfileNotFound
			}
			return nil, err
		}
		if status == "archived" {
			return nil, ErrAllianceProfileArchived
		}
		if expectedVersion > 0 && expectedVersion != version {
			return nil, ErrAllianceProfileConflict
		}
		var d allianceprofile.Document
		if strings.TrimSpace(doc) == "" || strings.TrimSpace(doc) == "{}" {
			d = allianceprofile.Template()
		} else if err := json.Unmarshal([]byte(doc), &d); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrAllianceProfileInvalid, err)
		}
		sug := item.Suggestion
		field := allianceprofile.Field{
			ID: sug.FieldID, Title: sug.FieldTitle, Value: value,
			Status: allianceprofile.StatusConfirmed,
			Source: fmt.Sprintf("feedback:%d/task:%d (人工确认)", item.ID, item.TaskID),
		}
		if err := d.Apply(sug.SectionID, sug.SectionTitle, sug.Phase, field); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrAllianceProfileInvalid, err)
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		newVersion := version + 1
		if _, err := tx.Exec(`UPDATE alliance_systems SET document=$2::jsonb, version=$3, updated_at=now() WHERE id=$1`,
			item.SystemID, string(raw), newVersion); err != nil {
			return nil, err
		}
		if err := insertAllianceRevisionTx(tx, item.SystemID, newVersion, name, environment, string(raw),
			fmt.Sprintf("feedback:%d/task:%d", item.ID, item.TaskID)); err != nil {
			return nil, err
		}
		item.AcceptedValue = value
		item.AcceptedVersion = &newVersion
	}
	if _, err := tx.Exec(`UPDATE alliance_profile_suggestions SET state=$2, accepted_value=$3, accepted_version=$4, reviewed_at=now()
WHERE id=$1`, item.ID, decision, item.AcceptedValue, item.AcceptedVersion); err != nil {
		return nil, err
	}
	item.State = decision
	item.ReviewedAt = time.Now().Format(time.RFC3339)
	return item, tx.Commit()
}

// AllianceSuggestionEvidence returns the evidence nodes referenced by a suggestion.
func (s *AllianceStore) AllianceSuggestionEvidence(id int64) ([]AllianceEvidence, error) {
	item, err := s.GetAllianceSuggestion(id)
	if err != nil {
		return nil, err
	}
	ids := item.Suggestion.EvidenceIDs
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT n.id, n.kind, n.payload::text
FROM exploration_nodes n JOIN tasks t ON t.exploration_id=n.exploration_id
WHERE t.id=$1 AND n.id=ANY($2::bigint[]) ORDER BY n.id`, item.TaskID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AllianceEvidence
	for rows.Next() {
		var e AllianceEvidence
		var payload string
		if err := rows.Scan(&e.ID, &e.Kind, &payload); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AllianceFeedbackRuns lists runs of one task newest first.
func (s *AllianceStore) AllianceFeedbackRuns(taskID int64) ([]*AllianceFeedbackRun, error) {
	rows, err := s.db.Query(`SELECT id, task_id, completed_at::text, state, error, attempt, updated_at::text
FROM alliance_profile_feedback_runs WHERE task_id=$1 ORDER BY id DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AllianceFeedbackRun
	for rows.Next() {
		r := &AllianceFeedbackRun{}
		if err := rows.Scan(&r.ID, &r.TaskID, &r.CompletedAt, &r.State, &r.Error, &r.Attempt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
