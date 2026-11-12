// Package store is the only place that talks to Postgres. Queries are
// hand-written against pgx; see docs/decisions.md.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/devashishbhat/totem/server/migrations"
)

// Store holds the connection pool.
type Store struct {
	Pool *pgxpool.Pool
}

// Open connects and verifies the connection.
func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse TM_DATABASE_URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.Pool.Close() }

// Migrate applies every embedded migration. It is safe to call on every boot.
func Migrate(ctx context.Context, url string) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return fmt.Errorf("parse TM_DATABASE_URL: %w", err)
	}
	db := stdlib.OpenDB(*cfg.ConnConfig)
	defer db.Close()
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
