package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/GermanCoding/matrix-transcribe-bot/internal/config"
	"github.com/GermanCoding/matrix-transcribe-bot/internal/matrix"
	"github.com/GermanCoding/matrix-transcribe-bot/internal/transcribe"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	bridge, err := transcribe.NewBridge(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer bridge.Close()

	bot, err := matrix.NewBot(cfg, bridge)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bot.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
