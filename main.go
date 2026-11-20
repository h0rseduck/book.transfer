package main

import (
	"book.transfer/src/config"
	"book.transfer/src/services/telegram"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	config.LoadEnv()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	db := config.GetDB()
	if err := db.Ping(); err != nil {
		panic(err)
	}
	defer db.Close()
	config.InitDB()

	transferService, err := telegram.NewTransferService()
	if err != nil {
		log.Fatal(err)
	}
	if err := transferService.ListenForWebhook(ctx); err != nil {
		log.Fatal(err)
	}
}
