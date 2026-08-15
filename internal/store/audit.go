package store

import (
	"time"
)

// AuditEntry 是审计日志条目。
type AuditEntry struct {
	ID     int64     `json:"id"`
	Time   time.Time `json:"time"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	Detail string    `json:"detail"`
}

// AddAudit 记录审计事件。
func (s *Store) AddAudit(actor, action, target, detail string) error {
	_, err := s.db.Exec(`INSERT INTO audit_log(ts, actor, action, target, detail) VALUES(?,?,?,?,?)`,
		nowStr(), actor, action, target, detail)
	return err
}

// ListAudit 列出最近审计事件。
func (s *Store) ListAudit(limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT id, ts, actor, action, target, detail FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ts string
		if err := rows.Scan(&e.ID, &ts, &e.Actor, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		e.Time, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneAudit 删除早于保留期的审计日志。
func (s *Store) PruneAudit(days int) error {
	if days <= 0 {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`DELETE FROM audit_log WHERE ts < ?`, cutoff)
	return err
}

// RequestSummary 是请求摘要（不含 Prompt/Response 正文）。
type RequestSummary struct {
	ID         int64     `json:"id"`
	Time       time.Time `json:"time"`
	RequestID  string    `json:"request_id"`
	Path       string    `json:"path"`
	Model      string    `json:"model"`
	Node       string    `json:"node"`
	Status     int       `json:"status"`
	TTFTMs     int64     `json:"ttft_ms"`
	DurationMs int64     `json:"duration_ms"`
	ErrorCode  string    `json:"error_code,omitempty"`
}

// AddRequestSummary 记录请求摘要。
func (s *Store) AddRequestSummary(r RequestSummary) error {
	_, err := s.db.Exec(`INSERT INTO request_summaries(ts, request_id, path, model, node, status, ttft_ms, duration_ms, error_code)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		nowStr(), r.RequestID, r.Path, r.Model, r.Node, r.Status, r.TTFTMs, r.DurationMs, r.ErrorCode)
	return err
}

// ListRequestSummaries 列出最近请求摘要。
func (s *Store) ListRequestSummaries(limit int) ([]RequestSummary, error) {
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT id, ts, request_id, path, model, node, status, ttft_ms, duration_ms, error_code
		FROM request_summaries ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestSummary
	for rows.Next() {
		var r RequestSummary
		var ts string
		if err := rows.Scan(&r.ID, &ts, &r.RequestID, &r.Path, &r.Model, &r.Node, &r.Status, &r.TTFTMs, &r.DurationMs, &r.ErrorCode); err != nil {
			return nil, err
		}
		r.Time, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneRequestSummaries 删除早于保留期的请求摘要。
func (s *Store) PruneRequestSummaries(days int) error {
	if days <= 0 {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`DELETE FROM request_summaries WHERE ts < ?`, cutoff)
	return err
}

// Summary 返回数据库概要（供总览页）。
func (s *Store) Summary() (map[string]int64, error) {
	out := map[string]int64{}
	var reqs, certs, audits int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM request_summaries`).Scan(&reqs); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM certs`).Scan(&certs); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&audits); err != nil {
		return nil, err
	}
	out["requests"] = reqs
	out["certs"] = certs
	out["audit"] = audits
	return out, nil
}
