// Package testsupport gives the server's tests a real Postgres to run against.
//
// Set TM_TEST_DATABASE_URL to a server the tests may create databases on. When
// it is unset the database-backed tests skip, so `go test ./...` still works on
// a machine with no Postgres.
package testsupport

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"

	"github.com/devashishbhat/totem/server/internal/store"
)

func init() {
	// Migration chatter would drown out the test output.
	goose.SetLogger(goose.NopLogger())
}

// DB creates a fresh, migrated database for one test and drops it afterwards.
func DB(t *testing.T) *store.Store {
	t.Helper()
	admin := os.Getenv("TM_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("TM_TEST_DATABASE_URL is not set")
	}
	name := fmt.Sprintf("totem_test_%d_%d", time.Now().UnixNano()%1e9, rand.N(1000))

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect to TM_TEST_DATABASE_URL: %v", err)
	}
	if _, err := conn.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("create database: %v", err)
	}
	_ = conn.Close(ctx)

	dsn := withDatabase(t, admin, name)
	if err := store.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		drop, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			return
		}
		defer func() { _ = drop.Close(context.Background()) }()
		_, _ = drop.Exec(context.Background(),
			"drop database if exists "+pgx.Identifier{name}.Sanitize()+" with (force)")
	})
	return db
}

func withDatabase(t *testing.T, dsn, name string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("TM_TEST_DATABASE_URL is not a URL: %v", err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}
