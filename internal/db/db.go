// Package db drží SQLite úložiště memů a jobů (WAL).
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type DB struct{ sql *sql.DB }

// migrations se aplikují popořadě; index+1 = user_version.
var migrations = []string{
	`CREATE TABLE memes (
		id TEXT PRIMARY KEY,          -- imgflip:<id> | reddit:<id>
		source TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		image_url TEXT NOT NULL DEFAULT '',
		local_path TEXT NOT NULL DEFAULT '',
		box_count INT NOT NULL DEFAULT 0,
		texts JSON NOT NULL DEFAULT '[]',
		format TEXT NOT NULL DEFAULT 'unknown',
		score REAL NOT NULL DEFAULT 0,
		fetched_at INT NOT NULL,
		rights TEXT NOT NULL DEFAULT 'template-only', -- nikdy nepublikovat originál
		classification JSON           -- výstup classify (beats, safe_for_brand…)
	);
	CREATE TABLE jobs (
		id TEXT PRIMARY KEY,
		meme_id TEXT NOT NULL REFERENCES memes(id),
		recipe TEXT NOT NULL,
		status TEXT NOT NULL,
		storyboard JSON,
		outputs JSON,
		metrics JSON,
		created_at INT NOT NULL,
		updated_at INT NOT NULL
	);
	CREATE INDEX jobs_meme ON jobs(meme_id);`,
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	s, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	s.SetMaxOpenConns(1)
	d := &DB{sql: s}
	if err := d.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error { return d.sql.Close() }

func (d *DB) migrate() error {
	var v int
	if err := d.sql.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := d.sql.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrace %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

type Meme struct {
	ID             string
	Source         string
	Title          string
	ImageURL       string
	LocalPath      string
	BoxCount       int
	Texts          []string
	Format         string
	Score          float64
	FetchedAt      time.Time
	Rights         string
	Classification json.RawMessage // výstup classify, nil = neklasifikováno
}

// UpsertMeme zachová lokální cestu, formát a klasifikaci, pokud je nový záznam nemá.
func (d *DB) UpsertMeme(ctx context.Context, m Meme) error {
	texts, _ := json.Marshal(nonNil(m.Texts))
	if m.Rights == "" {
		m.Rights = "template-only"
	}
	if m.Format == "" {
		m.Format = "unknown"
	}
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO memes (id, source, title, image_url, local_path, box_count, texts, format, score, fetched_at, rights)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, image_url=excluded.image_url,
			local_path=CASE WHEN excluded.local_path<>'' THEN excluded.local_path ELSE memes.local_path END,
			box_count=excluded.box_count, score=excluded.score, fetched_at=excluded.fetched_at,
			texts=CASE WHEN excluded.texts<>'[]' THEN excluded.texts ELSE memes.texts END`,
		m.ID, m.Source, m.Title, m.ImageURL, m.LocalPath, m.BoxCount, string(texts), m.Format, m.Score, m.FetchedAt.Unix(), m.Rights)
	return err
}

func (d *DB) SetClassification(ctx context.Context, id, format string, cls json.RawMessage) error {
	res, err := d.sql.ExecContext(ctx, `UPDATE memes SET format=?, classification=? WHERE id=?`, format, string(cls), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const memeCols = `id, source, title, image_url, local_path, box_count, texts, format, score, fetched_at, rights, classification`

func (d *DB) GetMeme(ctx context.Context, id string) (Meme, error) {
	m, err := scanMeme(d.sql.QueryRowContext(ctx, `SELECT `+memeCols+` FROM memes WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func (d *DB) ListMemes(ctx context.Context, source string, limit int) ([]Meme, error) {
	q := `SELECT ` + memeCols + ` FROM memes`
	args := []any{}
	if source != "" {
		q += ` WHERE source=?`
		args = append(args, source)
	}
	q += ` ORDER BY score DESC, id LIMIT ?`
	args = append(args, limit)
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Meme
	for rows.Next() {
		m, err := scanMeme(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanMeme(s scanner) (Meme, error) {
	var m Meme
	var texts string
	var fetched int64
	var cls sql.NullString
	if err := s.Scan(&m.ID, &m.Source, &m.Title, &m.ImageURL, &m.LocalPath, &m.BoxCount, &texts, &m.Format, &m.Score, &fetched, &m.Rights, &cls); err != nil {
		return m, err
	}
	json.Unmarshal([]byte(texts), &m.Texts)
	m.FetchedAt = time.Unix(fetched, 0)
	if cls.Valid && cls.String != "" {
		m.Classification = json.RawMessage(cls.String)
	}
	return m, nil
}

type Job struct {
	ID         string
	MemeID     string
	Recipe     string
	Status     string
	Storyboard json.RawMessage
	Outputs    json.RawMessage
	Metrics    json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (d *DB) UpsertJob(ctx context.Context, j Job) error {
	now := time.Now().Unix()
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO jobs (id, meme_id, recipe, status, storyboard, outputs, metrics, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET status=excluded.status,
			storyboard=COALESCE(excluded.storyboard, jobs.storyboard),
			outputs=COALESCE(excluded.outputs, jobs.outputs),
			metrics=COALESCE(excluded.metrics, jobs.metrics),
			updated_at=excluded.updated_at`,
		j.ID, j.MemeID, j.Recipe, j.Status, nullJSON(j.Storyboard), nullJSON(j.Outputs), nullJSON(j.Metrics), now, now)
	return err
}

func (d *DB) GetJob(ctx context.Context, id string) (Job, error) {
	var j Job
	var sb, out, met sql.NullString
	var c, u int64
	err := d.sql.QueryRowContext(ctx, `SELECT id, meme_id, recipe, status, storyboard, outputs, metrics, created_at, updated_at FROM jobs WHERE id=?`, id).
		Scan(&j.ID, &j.MemeID, &j.Recipe, &j.Status, &sb, &out, &met, &c, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	j.Storyboard, j.Outputs, j.Metrics = raw(sb), raw(out), raw(met)
	j.CreatedAt, j.UpdatedAt = time.Unix(c, 0), time.Unix(u, 0)
	return j, err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nullJSON(r json.RawMessage) any {
	if len(r) == 0 {
		return nil
	}
	return string(r)
}

func raw(s sql.NullString) json.RawMessage {
	if !s.Valid {
		return nil
	}
	return json.RawMessage(s.String)
}
