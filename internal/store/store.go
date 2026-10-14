// Package store keeps events and delivery attempts in SQLite.
//
// Durability is the whole point: Insert commits (WAL, synchronous=FULL) before the relay tells the
// provider "got it", so an event that was acknowledged survives a crash.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no C compiler needed
)

// Event statuses.
const (
	Pending   = "pending"   // stored, waiting for its first attempt
	Retrying  = "retrying"  // failed, waiting for next_at
	Delivered = "delivered" // the app accepted it
	Held      = "held"      // stopped; kept safe until a person resends it
	Duplicate = "duplicate" // a copy of an event already received; never forwarded
)

// Signature results recorded at arrival.
const (
	SigValid     = "valid"
	SigInvalid   = "invalid"
	SigMissing   = "missing"
	SigMalformed = "malformed"
	SigStale     = "stale"
	SigUnchecked = "unchecked" // the check is not written yet (level 1 locked)
	SigNone      = "none"      // the route has no secret
)

// SignatureFailed reports whether a signature result means "do not forward".
func SignatureFailed(sig string) bool {
	switch sig {
	case SigInvalid, SigMissing, SigMalformed, SigStale:
		return true
	}
	return false
}

// Event is one webhook as received.
type Event struct {
	ID         int64
	Route      string
	Source     string
	EventType  string
	ReceivedAt time.Time
	Headers    http.Header
	Body       []byte
	Signature  string
	DedupeKey  string
	Status     string
	Attempts   int
	NextAt     time.Time
	Reason     string // latest plain-language reason for the status
	Diagnosis  string // JSON from package diagnose; empty until diagnosed
	UpdatedAt  time.Time
}

// Attempt is one delivery try.
type Attempt struct {
	ID         int64
	EventID    int64
	At         time.Time
	StatusCode int
	TimedOut   bool
	ConnFailed bool
	Duration   time.Duration
	Response   string
	Action     string
	Reason     string
}

// ErrNotFound is returned for a missing event.
var ErrNotFound = errors.New("event not found")

// Store wraps the database.
type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS events(
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	route       TEXT    NOT NULL,
	source      TEXT    NOT NULL,
	event_type  TEXT    NOT NULL DEFAULT '',
	received_at INTEGER NOT NULL,
	headers     TEXT    NOT NULL,
	body        BLOB    NOT NULL,
	signature   TEXT    NOT NULL,
	dedupe_key  TEXT    NOT NULL DEFAULT '',
	status      TEXT    NOT NULL,
	attempts    INTEGER NOT NULL DEFAULT 0,
	next_at     INTEGER NOT NULL DEFAULT 0,
	reason      TEXT    NOT NULL DEFAULT '',
	diagnosis   TEXT    NOT NULL DEFAULT '',
	updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS events_due   ON events(status, next_at);
CREATE INDEX IF NOT EXISTS events_route ON events(route, id);
CREATE INDEX IF NOT EXISTS events_key   ON events(dedupe_key);
CREATE TABLE IF NOT EXISTS attempts(
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id    INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
	at          INTEGER NOT NULL,
	status_code INTEGER NOT NULL,
	timed_out   INTEGER NOT NULL,
	conn_failed INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL,
	response    TEXT    NOT NULL,
	action      TEXT    NOT NULL,
	reason      TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS attempts_event ON attempts(event_id, id);
`

// Open opens (and creates) the database at path. Use ":memory:" in tests.
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		dsn = "file:" + path
	}
	dsn += sep(dsn) + "_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite serialises writes anyway, and ":memory:" is per connection.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

func sep(dsn string) string {
	if strings.Contains(dsn, "?") {
		return "&"
	}
	return "?"
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

// Insert stores a new event and returns its id. It returns only after the commit is on disk.
func (s *Store) Insert(ctx context.Context, e *Event) (int64, error) {
	h, err := json.Marshal(e.Headers)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO events
		(route, source, event_type, received_at, headers, body, signature, dedupe_key, status, attempts, next_at, reason, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,0,?,?,?)`,
		e.Route, e.Source, e.EventType, ms(e.ReceivedAt), string(h), e.Body, e.Signature, e.DedupeKey,
		e.Status, ms(e.NextAt), e.Reason, ms(now))
	if err != nil {
		return 0, err
	}
	e.ID, err = res.LastInsertId()
	return e.ID, err
}

const eventCols = `id, route, source, event_type, received_at, headers, body, signature, dedupe_key,
	status, attempts, next_at, reason, diagnosis, updated_at`

func scanEvent(row interface{ Scan(...any) error }) (Event, error) {
	var e Event
	var received, next, updated int64
	var headers string
	err := row.Scan(&e.ID, &e.Route, &e.Source, &e.EventType, &received, &headers, &e.Body, &e.Signature,
		&e.DedupeKey, &e.Status, &e.Attempts, &next, &e.Reason, &e.Diagnosis, &updated)
	if err != nil {
		return e, err
	}
	e.ReceivedAt, e.NextAt, e.UpdatedAt = fromMs(received), fromMs(next), fromMs(updated)
	if err := json.Unmarshal([]byte(headers), &e.Headers); err != nil {
		return e, fmt.Errorf("event %d headers: %w", e.ID, err)
	}
	return e, nil
}

func (s *Store) query(ctx context.Context, q string, args ...any) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get returns one event.
func (s *Store) Get(ctx context.Context, id int64) (Event, error) {
	e, err := scanEvent(s.db.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// Due returns events whose next attempt is due, oldest first.
func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]Event, error) {
	return s.query(ctx, `SELECT `+eventCols+` FROM events
		WHERE status IN (?, ?) AND next_at <= ? ORDER BY next_at, id LIMIT ?`,
		Pending, Retrying, ms(now), limit)
}

// List returns the newest events for the given routes.
func (s *Store) List(ctx context.Context, routes []string, limit int) ([]Event, error) {
	if len(routes) == 0 {
		return nil, nil
	}
	q := `SELECT ` + eventCols + ` FROM events WHERE route IN (?` + strings.Repeat(",?", len(routes)-1) + `)
		ORDER BY id DESC LIMIT ?`
	args := make([]any, 0, len(routes)+1)
	for _, r := range routes {
		args = append(args, r)
	}
	return s.query(ctx, q, append(args, limit)...)
}

// ByStatus returns a route's events with one status, oldest first.
func (s *Store) ByStatus(ctx context.Context, route, status string) ([]Event, error) {
	return s.query(ctx, `SELECT `+eventCols+` FROM events WHERE route = ? AND status = ? ORDER BY id`, route, status)
}

// Undiagnosed returns held events that have no diagnosis yet (for a restart).
func (s *Store) Undiagnosed(ctx context.Context) ([]Event, error) {
	return s.query(ctx, `SELECT `+eventCols+` FROM events WHERE status = ? AND diagnosis = '' ORDER BY id`, Held)
}

// LastDelivered returns the most recent delivered event of the same route and type received before id.
func (s *Store) LastDelivered(ctx context.Context, route, eventType string, before int64) (Event, bool, error) {
	e, err := scanEvent(s.db.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events
		WHERE route = ? AND event_type = ? AND status = ? AND id < ? ORDER BY id DESC LIMIT 1`,
		route, eventType, Delivered, before))
	if errors.Is(err, sql.ErrNoRows) {
		return e, false, nil
	}
	return e, err == nil, err
}

// HasKey reports whether a non-duplicate event with this dedupe key already exists in the route.
func (s *Store) HasKey(ctx context.Context, route, key string) (int64, bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM events WHERE route = ? AND dedupe_key = ? AND status != ?
		ORDER BY id LIMIT 1`, route, key, Duplicate).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// RecordAttempt stores an attempt and moves the event to its new status in one transaction.
func (s *Store) RecordAttempt(ctx context.Context, a Attempt, status string, nextAt time.Time, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempts
		(event_id, at, status_code, timed_out, conn_failed, duration_ms, response, action, reason)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		a.EventID, ms(a.At), a.StatusCode, a.TimedOut, a.ConnFailed, a.Duration.Milliseconds(), a.Response,
		a.Action, a.Reason); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE events SET status = ?, attempts = attempts + 1, next_at = ?,
		reason = ?, updated_at = ? WHERE id = ?`, status, ms(nextAt), reason, ms(time.Now()), a.EventID); err != nil {
		return err
	}
	return tx.Commit()
}

// Requeue puts a held event back in line for delivery with a fresh attempt count.
// The attempt history is kept.
func (s *Store) Requeue(ctx context.Context, id int64, reason string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE events SET status = ?, attempts = 0, next_at = ?, reason = ?,
		diagnosis = '', updated_at = ? WHERE id = ? AND status = ?`,
		Pending, ms(time.Now()), reason, ms(time.Now()), id, Held)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("event %d is not held", id)
	}
	return nil
}

// SetReason updates the plain-language reason without changing the status.
func (s *Store) SetReason(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE events SET reason = ?, updated_at = ? WHERE id = ?`, reason, ms(time.Now()), id)
	return err
}

// SetDiagnosis stores the diagnosis JSON.
func (s *Store) SetDiagnosis(ctx context.Context, id int64, diagnosis string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE events SET diagnosis = ?, updated_at = ? WHERE id = ?`, diagnosis, ms(time.Now()), id)
	return err
}

// Attempts returns an event's attempts, oldest first.
func (s *Store) Attempts(ctx context.Context, eventID int64) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, event_id, at, status_code, timed_out, conn_failed, duration_ms,
		response, action, reason FROM attempts WHERE event_id = ? ORDER BY id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var a Attempt
		var at, dur int64
		if err := rows.Scan(&a.ID, &a.EventID, &at, &a.StatusCode, &a.TimedOut, &a.ConnFailed, &dur,
			&a.Response, &a.Action, &a.Reason); err != nil {
			return nil, err
		}
		a.At, a.Duration = fromMs(at), time.Duration(dur)*time.Millisecond
		out = append(out, a)
	}
	return out, rows.Err()
}

// Counts returns how many events of a route are in each status.
func (s *Store) Counts(ctx context.Context, route string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM events WHERE route = ? GROUP BY status`, route)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// Prune deletes events received before cutoff.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE received_at < ?`, ms(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
