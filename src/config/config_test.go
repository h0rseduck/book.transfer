package config

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestInitDBPreservesExistingUsers(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "database.db"))
	if err != nil {
		t.Fatalf("open temporary database: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE user (id INTEGER NOT NULL PRIMARY KEY, email TEXT)`); err != nil {
		t.Fatalf("create legacy user table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user (id, email) VALUES (?, ?), (?, ?)`,
		101, "first@kindle.com", 202, "second@kindle.com"); err != nil {
		t.Fatalf("seed legacy users: %v", err)
	}

	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })

	InitDB()

	rows, err := db.Query(`SELECT id, email FROM user ORDER BY id`)
	if err != nil {
		t.Fatalf("query users after migration: %v", err)
	}
	defer rows.Close()

	want := []struct {
		id    int64
		email string
	}{
		{101, "first@kindle.com"},
		{202, "second@kindle.com"},
	}

	var got int
	for rows.Next() {
		if got >= len(want) {
			t.Fatal("migration added an unexpected user")
		}
		var id int64
		var email string
		if err := rows.Scan(&id, &email); err != nil {
			t.Fatalf("scan user: %v", err)
		}
		if id != want[got].id || email != want[got].email {
			t.Fatalf("user %d changed: got (%d, %q), want (%d, %q)", got, id, email, want[got].id, want[got].email)
		}
		got++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate users: %v", err)
	}
	if got != len(want) {
		t.Fatalf("migration preserved %d users, want %d", got, len(want))
	}

	for _, table := range []string{"bot_document", "document_transfer"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("new table %q was not created: %v", table, err)
		}
	}

	now := time.Unix(2_000_000_000, 0)
	old := now.Add(-ServiceDataRetention - time.Hour).Unix()
	recent := now.Add(-ServiceDataRetention + time.Hour).Unix()
	for _, createdAt := range []int64{old, recent} {
		if _, err := db.Exec(
			`INSERT INTO bot_document(chat_id, message_id, file_id, file_unique_id, file_name, caption, source_bot_id, created_at)
			 VALUES(?, ?, 'file', 'unique', 'book.epub', '', 300, ?)`,
			createdAt, createdAt, createdAt,
		); err != nil {
			t.Fatalf("seed bot document: %v", err)
		}
		if _, err := db.Exec(
			`INSERT INTO document_transfer(chat_id, message_id, user_id, sent_at) VALUES(?, ?, 101, ?)`,
			createdAt, createdAt, createdAt,
		); err != nil {
			t.Fatalf("seed document transfer: %v", err)
		}
	}

	if err := PruneServiceData(now.Add(-ServiceDataRetention)); err != nil {
		t.Fatalf("prune service data: %v", err)
	}
	for _, table := range []string{"bot_document", "document_transfer"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s after pruning: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s contains %d rows after pruning, want only the recent row", table, count)
		}
	}
}
