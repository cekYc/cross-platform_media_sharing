package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/discord"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/telegram"

	"github.com/joho/godotenv"
)

const defaultQueueSize = 100

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

	discord.InitBot(dcBotToken)
	defer discord.Session.Close()

	queueSize := resolveQueueSize()
	mediaQueue := make(chan models.MediaEvent, queueSize)

	go discord.StartConsumer(mediaQueue)

	log.Printf("[+] Telegram producer started (queue size: %d)", queueSize)
	telegram.StartProducer(tgToken, mediaQueue)
}

func resolveQueueSize() int {
	queueSize := defaultQueueSize
	value := strings.TrimSpace(os.Getenv("MEDIA_QUEUE_SIZE"))
	if value == "" {
		return queueSize
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Printf("[WARN] invalid MEDIA_QUEUE_SIZE value (%q), using default: %d", value, defaultQueueSize)
		return queueSize
	}

	return parsed
}
