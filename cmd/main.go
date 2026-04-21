package main

import (
	"log"
	"os"
	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/discord"
	"tg-discord-bot/internal/observability"
	"tg-discord-bot/internal/telegram"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("[WARN] .env file not found, falling back to process environment variables")
	}

	tgToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	dcBotToken := os.Getenv("DISCORD_BOT_TOKEN")

	if tgToken == "" || dcBotToken == "" {
		log.Fatal("missing required environment variables: TELEGRAM_BOT_TOKEN and/or DISCORD_BOT_TOKEN")
	}

	log.Println("starting bridge service...")

	database.InitDB()
	defer database.DB.Close()

	observability.Start()

	discord.InitBot(dcBotToken)
	defer discord.Session.Close()

	go discord.StartConsumer()

	log.Printf("[+] Telegram producer started")
	telegram.StartProducer(tgToken)
}
