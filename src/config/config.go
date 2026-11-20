package config

import (
	"database/sql"
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
	if errorENV != nil {
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
			sent_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
			PRIMARY KEY (chat_id, message_id, user_id)
		);`,
	}

	for _, statement := range statements {
		if _, err := DB.Exec(statement); err != nil {
			panic(fmt.Sprintf("%q: %s\n", err, statement))
		}
	}

	if err := PruneServiceData(time.Now().Add(-ServiceDataRetention)); err != nil {
		panic(fmt.Sprintf("failed to prune service data: %q", err))
	}
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
