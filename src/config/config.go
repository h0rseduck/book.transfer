package config

import (
	"database/sql"
	"fmt"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"os"
)

var DB *sql.DB

func LoadEnv() {
	errorENV := godotenv.Load()
	if errorENV != nil {
		panic("Failed to load env file")
	}
}

func InitDB() {
	sql := `CREATE TABLE IF NOT EXISTS user (
		id INTEGER not null primary key,
		email text
	);`
	_, err := DB.Exec(sql)
	if err != nil {
		panic(fmt.Sprintf("%q: %s\n", err, sql))
	}
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
