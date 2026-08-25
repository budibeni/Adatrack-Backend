package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Audit log (GAP #12 / B4 4.2) — catat login sukses/gagal, akses 403, dan
// aksi admin ke tabel master.audit_logs.
//
// Desain:
//   - ASYNC via worker tunggal + buffer channel agar audit tidak pernah
//     memperlambat path request.
//   - TIDAK silent-drop: bila buffer penuh / INSERT gagal → slog.Error
//     (jejak tetap ada di log terstruktur).
//   - Best-effort pada shutdown: CloseAudit() men-drain sisa antrean.
// ---------------------------------------------------------------------------

// AuditEntry is one audit record written to master.audit_logs.
type AuditEntry struct {
	UserID      uint64 // 0 = belum dikenal (mis. login gagal email tak terdaftar)
	CompanyCode string
	EventType   string // LOGIN_SUCCESS | LOGIN_FAILURE | ACCESS_DENIED | TOKEN_REVOKED | ADMIN_ACTION
	Action      string // mis. "login", "vehicle.create", "geofence.delete"
	Entity      string // mis. "vehicle"
	EntityID    string // string agar bisa memuat IMEI/uuid/id apa pun
	IP          string
	UserAgent   string
	Details     map[string]interface{}
}

type auditJob struct {
	db  *sql.DB
	ent AuditEntry
}

var (
	auditOnce   sync.Once
	auditCh     chan auditJob
	auditCloser chan struct{}
)

const (
	auditBufferSize    = 256
	auditInsertTimeout = 5 * time.Second
)

// LogAudit queues an audit entry asynchronously. Lazily starts the worker.
// Never blocks the caller; when the queue is full the entry is logged as an
// error instead of being silently dropped.
func LogAudit(db *sql.DB, e AuditEntry) {
	if db == nil {
		slog.Error("audit: nil db, entry dropped-with-log", "event_type", e.EventType, "action", e.Action)
		return
	}
	auditOnce.Do(startAuditWorker)
	job := auditJob{db: db, ent: e}
	select {
	case auditCh <- job:
	default:
		slog.Error("audit: queue full — entry dropped-with-log",
			"event_type", e.EventType, "action", e.Action,
			"user_id", e.UserID, "company", e.CompanyCode)
	}
}

// CloseAudit drains the audit queue (best-effort) and stops the worker.
func CloseAudit() {
	if auditCloser == nil {
		return
	}
	close(auditCloser)
}

func startAuditWorker() {
	auditCh = make(chan auditJob, auditBufferSize)
	auditCloser = make(chan struct{})
	go func() {
		for {
			select {
			case <-auditCloser:
				// Drain sisa antrean sebelum keluar.
				for {
					select {
					case j := <-auditCh:
						writeAudit(j)
					default:
						return
					}
				}
			case j := <-auditCh:
				writeAudit(j)
			}
		}
	}()
}

func writeAudit(j auditJob) {
	ctx, cancel := context.WithTimeout(context.Background(), auditInsertTimeout)
	defer cancel()

	var details []byte
	if len(j.ent.Details) > 0 {
		b, err := json.Marshal(j.ent.Details)
		if err == nil {
			details = b
		} else {
			slog.Warn("audit: marshal details gagal", "error", err)
		}
	}
	detailsArg := interface{}(nil)
	if details != nil {
		detailsArg = string(details)
	}

	_, err := j.db.ExecContext(ctx, `INSERT INTO audit_logs
		(user_id, company_code, event_type, action, entity, entity_id, ip_address, user_agent, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullUint(j.ent.UserID), nullStrA(j.ent.CompanyCode), j.ent.EventType, j.ent.Action,
		nullStrA(j.ent.Entity), nullStrA(j.ent.EntityID),
		nullStrA(j.ent.IP), nullStrA(j.ent.UserAgent), detailsArg)
	if err != nil {
		slog.Error("audit: insert gagal — entry logged-not-silent",
			"error", err, "event_type", j.ent.EventType, "action", j.ent.Action,
			"user_id", j.ent.UserID, "company", j.ent.CompanyCode)
	}
}

func nullUint(id uint64) interface{} {
	if id == 0 {
		return nil
	}
	return id
}

func nullStrA(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
