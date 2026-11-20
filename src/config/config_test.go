package config

import (
	"database/sql"
	"fmt"
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
	if _, err := db.Exec(`CREATE TABLE document_transfer (
		chat_id INTEGER NOT NULL,
		message_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		sent_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		PRIMARY KEY (chat_id, message_id, user_id)
	)`); err != nil {
		t.Fatalf("create legacy document_transfer table: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE bot_document (
		chat_id INTEGER NOT NULL,
		message_id INTEGER NOT NULL,
		file_id TEXT NOT NULL,
		file_unique_id TEXT NOT NULL,
		file_name TEXT NOT NULL,
		caption TEXT NOT NULL,
		source_bot_id INTEGER NOT NULL,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		PRIMARY KEY (chat_id, message_id)
	)`); err != nil {
		t.Fatalf("create legacy bot_document table: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO bot_document(chat_id, message_id, file_id, file_unique_id, file_name, caption, source_bot_id)
		 VALUES(1, 10, 'file-1', 'legacy-same-file', 'book.epub', '', 300),
		       (1, 11, 'file-2', 'legacy-same-file', 'book.epub', '', 300)`,
	); err != nil {
		t.Fatalf("seed legacy bot documents: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO document_transfer(chat_id, message_id, user_id) VALUES(1, 10, 101), (1, 11, 101)`,
	); err != nil {
		t.Fatalf("seed legacy transfers: %v", err)
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
	var migratedColumnCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('document_transfer') WHERE name = 'file_unique_id'`,
	).Scan(&migratedColumnCount); err != nil {
		t.Fatalf("inspect migrated document_transfer: %v", err)
	}
	if migratedColumnCount != 1 {
		t.Fatalf("file_unique_id migration count = %d, want 1", migratedColumnCount)
	}
	var migratedTransferCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM document_transfer WHERE user_id = 101 AND file_unique_id = 'legacy-same-file'`,
	).Scan(&migratedTransferCount); err != nil {
		t.Fatalf("count migrated transfers: %v", err)
	}
	if migratedTransferCount != 1 {
		t.Fatalf("migrated duplicate transfer count = %d, want 1", migratedTransferCount)
	}

	first, err := db.Exec(
		`INSERT OR IGNORE INTO document_transfer(chat_id, message_id, user_id, file_unique_id) VALUES(1, 1, 101, 'same-file')`,
	)
	if err != nil {
		t.Fatalf("insert first file transfer: %v", err)
	}
	second, err := db.Exec(
		`INSERT OR IGNORE INTO document_transfer(chat_id, message_id, user_id, file_unique_id) VALUES(1, 2, 101, 'same-file')`,
	)
	if err != nil {
		t.Fatalf("insert duplicate file transfer: %v", err)
	}
	firstRows, _ := first.RowsAffected()
	secondRows, _ := second.RowsAffected()
	if firstRows != 1 || secondRows != 0 {
		t.Fatalf("file deduplication rows = (%d, %d), want (1, 0)", firstRows, secondRows)
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
			`INSERT INTO document_transfer(chat_id, message_id, user_id, file_unique_id, sent_at) VALUES(?, ?, 202, ?, ?)`,
			createdAt, createdAt, fmt.Sprintf("file-%d", createdAt), createdAt,
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
