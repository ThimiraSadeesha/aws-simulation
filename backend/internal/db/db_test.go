package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateIdempotentAndIntegrity(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	applied, err := d.Migrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 {
		t.Fatal("expected migrations to apply")
	}
	again, err := d.Migrate(ctx)
	if err != nil || len(again) != 0 {
		t.Fatalf("second migrate applied %v err %v", again, err)
	}
	var fk int
	if err := d.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign keys not enabled: %d %v", fk, err)
	}
	rep, err := d.Integrity(ctx)
	if err != nil || rep.IntegrityCheck != "ok" {
		t.Fatalf("integrity %+v %v", rep, err)
	}
	dst := filepath.Join(t.TempDir(), "backup.db")
	if err := d.Backup(ctx, dst); err != nil {
		t.Fatal(err)
	}
	b, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var n int
	if err := b.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n != len(applied) {
		t.Fatalf("backup migrations %d %v", n, err)
	}
}

func TestForeignKeyEnforced(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer d.Close()
	if _, err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := d.Exec(`INSERT INTO sessions(id_hash,user_id,csrf_token,created_at,expires_at,last_seen) VALUES ('x',999,'c','a','b','c')`)
	if err == nil {
		t.Fatal("expected FK violation")
	}
}
