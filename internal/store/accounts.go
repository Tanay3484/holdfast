package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// User is a person signed in with GitHub. Token is sealed by package session before it gets here.
type User struct {
	ID       int64
	GitHubID int64
	Login    string
	Name     string
	Avatar   string
	Token    string
}

// Connection is a Live route a user created. Secret is sealed by package session.
type Connection struct {
	Name        string // also the route name: providers send to /in/<Name>
	UserID      int64
	Source      string // github | stripe | generic
	Label       string // shown to the user, e.g. "tanay/holdfast"
	Secret      string
	Destination string // the user's app; empty when TestApp is set
	TestApp     bool   // deliver to Holdfast's built-in test app instead
	Repo        string // GitHub repository the webhook was added to
	HookID      int64  // GitHub's id for that webhook
	CreatedAt   time.Time
}

// UpsertUser creates or updates the user with this GitHub id and returns our id.
func (s *Store) UpsertUser(ctx context.Context, u User) (int64, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (github_id, login, name, avatar, token, created_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(github_id) DO UPDATE SET login = excluded.login, name = excluded.name,
			avatar = excluded.avatar, token = excluded.token`,
		u.GitHubID, u.Login, u.Name, u.Avatar, u.Token, ms(time.Now()))
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE github_id = ?`, u.GitHubID).Scan(&id)
	return id, err
}

// GetUser returns a user by our id.
func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT id, github_id, login, name, avatar, token FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.GitHubID, &u.Login, &u.Name, &u.Avatar, &u.Token)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

const connCols = `name, user_id, source, label, secret, destination, test_app, repo, hook_id, created_at`

func scanConn(row interface{ Scan(...any) error }) (Connection, error) {
	var c Connection
	var created int64
	err := row.Scan(&c.Name, &c.UserID, &c.Source, &c.Label, &c.Secret, &c.Destination, &c.TestApp, &c.Repo, &c.HookID, &created)
	c.CreatedAt = fromMs(created)
	return c, err
}

func (s *Store) conns(ctx context.Context, q string, args ...any) ([]Connection, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddConnection stores a new connection.
func (s *Store) AddConnection(ctx context.Context, c Connection) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO connections (`+connCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		c.Name, c.UserID, c.Source, c.Label, c.Secret, c.Destination, c.TestApp, c.Repo, c.HookID, ms(time.Now()))
	return err
}

// Connection returns one connection.
func (s *Store) Connection(ctx context.Context, name string) (Connection, error) {
	c, err := scanConn(s.db.QueryRowContext(ctx, `SELECT `+connCols+` FROM connections WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// Connections returns a user's connections, newest first.
func (s *Store) Connections(ctx context.Context, userID int64) ([]Connection, error) {
	return s.conns(ctx, `SELECT `+connCols+` FROM connections WHERE user_id = ? ORDER BY created_at DESC`, userID)
}

// AllConnections returns every connection, for loading routes at start-up.
func (s *Store) AllConnections(ctx context.Context) ([]Connection, error) {
	return s.conns(ctx, `SELECT `+connCols+` FROM connections ORDER BY created_at`)
}

// SetConnectionSecret replaces a connection's sealed secret.
func (s *Store) SetConnectionSecret(ctx context.Context, name, sealed string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE connections SET secret = ? WHERE name = ?`, sealed, name)
	return err
}

// DeleteConnection removes a connection and every event it received.
func (s *Store) DeleteConnection(ctx context.Context, name string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE route = ?`, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM connections WHERE name = ?`, name); err != nil {
		return err
	}
	return tx.Commit()
}
