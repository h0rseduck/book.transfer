package main

import (
	"book.transfer/src/config"
	"book.transfer/src/services/telegram"
)

func main() {
	config.LoadEnv()

	db := config.GetDB()
	if err := db.Ping(); err != nil {
		panic(err)
	}
	defer db.Close()
	config.InitDB()

	transferService := telegram.NewTransferService()
	transferService.ListenForWebhook()
	//transferService.Observe()
}
