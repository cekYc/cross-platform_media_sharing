package main

import (
	"log"
	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/discord"
	"tg-discord-bot/internal/observability"
	"tg-discord-bot/internal/queue"
	"tg-discord-bot/internal/security"
	"tg-discord-bot/internal/telegram"
	_ "tg-discord-bot/internal/webhook"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("[WARN] .env file not found, falling back to process environment variables")
	}

	tgToken := security.LoadSecret("TELEGRAM_BOT_TOKEN")
	dcBotToken := security.LoadSecret("DISCORD_BOT_TOKEN")

	if tgToken == "" || dcBotToken == "" {
		log.Fatal("missing required environment variables: TELEGRAM_BOT_TOKEN and/or DISCORD_BOT_TOKEN")
	}

	log.Println("starting bridge service...")

	database.InitDB()
	defer database.DB.Close()

	observability.Start()

	// Initialize adapters
	discord.InitBot(dcBotToken)
	defer discord.Session.Close()

	tgProducer := telegram.NewProducer(tgToken)

	// Start generic queue processor
	go queue.StartProcessor()

	// Start Producers
	log.Printf("[+] Telegram producer started")
	tgProducer.Start() // Blocking
}
