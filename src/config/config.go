package config

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

const ServiceDataRetention = 30 * 24 * time.Hour

func LoadEnv() {
	errorENV := godotenv.Load()
	if errorENV != nil && !errors.Is(errorENV, os.ErrNotExist) {
		panic("Failed to load env file")
	}
}

func InitDB() {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS user (
			id INTEGER NOT NULL PRIMARY KEY,
			email TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS bot_document (
			chat_id INTEGER NOT NULL,
			message_id INTEGER NOT NULL,
			file_id TEXT NOT NULL,
			file_unique_id TEXT NOT NULL,
			file_name TEXT NOT NULL,
			caption TEXT NOT NULL,
			source_bot_id INTEGER NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
			PRIMARY KEY (chat_id, message_id)
		);`,
		`CREATE TABLE IF NOT EXISTS document_transfer (
			chat_id INTEGER NOT NULL,
			message_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			file_unique_id TEXT NOT NULL DEFAULT '',
			sent_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
			PRIMARY KEY (chat_id, message_id, user_id)
		);`,
	}

	for _, statement := range statements {
		if _, err := DB.Exec(statement); err != nil {
			panic(fmt.Sprintf("%q: %s\n", err, statement))
		}
	}
	if err := ensureDocumentTransferFileUniqueID(); err != nil {
		panic(fmt.Sprintf("failed to migrate document transfers: %q", err))
	}

	if err := PruneServiceData(time.Now().Add(-ServiceDataRetention)); err != nil {
		panic(fmt.Sprintf("failed to prune service data: %q", err))
	}
}

func ensureDocumentTransferFileUniqueID() error {
	var count int
	if err := DB.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('document_transfer') WHERE name = 'file_unique_id'`,
	).Scan(&count); err != nil {
		return fmt.Errorf("inspect document_transfer columns: %w", err)
	}
	if count == 0 {
		if _, err := DB.Exec(`ALTER TABLE document_transfer ADD COLUMN file_unique_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add file_unique_id: %w", err)
		}
	}
	if _, err := DB.Exec(
		`UPDATE document_transfer
		 SET file_unique_id = COALESCE((
			SELECT bot_document.file_unique_id
			FROM bot_document
			WHERE bot_document.chat_id = document_transfer.chat_id
			  AND bot_document.message_id = document_transfer.message_id
		 ), '')
		 WHERE file_unique_id = ''`,
	); err != nil {
		return fmt.Errorf("backfill file_unique_id: %w", err)
	}
	if _, err := DB.Exec(
		`DELETE FROM document_transfer
		 WHERE file_unique_id <> ''
		   AND rowid NOT IN (
			SELECT MIN(rowid)
			FROM document_transfer
			WHERE file_unique_id <> ''
			GROUP BY user_id, file_unique_id
		   )`,
	); err != nil {
		return fmt.Errorf("remove legacy duplicate transfers: %w", err)
	}
	if _, err := DB.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS document_transfer_user_file_unique
		 ON document_transfer(user_id, file_unique_id)
		 WHERE file_unique_id <> ''`,
	); err != nil {
		return fmt.Errorf("create file deduplication index: %w", err)
	}
	return nil
}

func PruneServiceData(olderThan time.Time) error {
	transaction, err := DB.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()

	cutoff := olderThan.Unix()
	if _, err := transaction.Exec(`DELETE FROM document_transfer WHERE sent_at < ?`, cutoff); err != nil {
		return fmt.Errorf("prune document transfers: %w", err)
	}
	if _, err := transaction.Exec(`DELETE FROM bot_document WHERE created_at < ?`, cutoff); err != nil {
		return fmt.Errorf("prune bot documents: %w", err)
	}

	return transaction.Commit()
}

func GetDB() *sql.DB {
	if DB == nil {
		path, _ := os.Getwd()
		db, err := sql.Open("sqlite3", path+"/database/database.db")
		if err != nil {
			panic(err)
		}
		DB = db
	}
	return DB
}
