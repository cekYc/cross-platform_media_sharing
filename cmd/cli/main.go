package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"tg-discord-bot/internal/database"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	command := os.Args[1]

	database.InitDB()
	// InitDB opens sqlite "bot.db" in the current directory. Make sure you run this from the project root.

	switch command {
	case "add-webhook":
		addWebhookCmd := flag.NewFlagSet("add-webhook", flag.ExitOnError)
		sourcePlatform := addWebhookCmd.String("source-platform", "telegram", "Source platform (telegram/discord)")
		sourceID := addWebhookCmd.String("source-id", "", "Source chat/channel ID")
		targetURL := addWebhookCmd.String("url", "", "Target webhook URL")
		secret := addWebhookCmd.String("secret", "", "Webhook HMAC secret")

		addWebhookCmd.Parse(os.Args[2:])

		if *sourceID == "" || *targetURL == "" {
			fmt.Println("Error: -source-id and -url are required")
			addWebhookCmd.PrintDefaults()
			return
		}

		err := database.LinkChannel(*sourcePlatform, *sourceID, "webhook", *targetURL, *secret)
		if err != nil {
			log.Fatalf("Failed to add webhook: %v", err)
		}
		fmt.Printf("Successfully linked %s:%s to webhook %s\n", *sourcePlatform, *sourceID, *targetURL)

	case "list":
		fmt.Println("Listing all pairings is not fully implemented in CLI yet.")
		// We could implement a direct SQL query here if needed

	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
	}
}

func printUsage() {
	usage := `
tg-discord-bot CLI Management Tool

Usage:
  go run cmd/cli/main.go <command> [arguments]

Commands:
  add-webhook   Link a source to a webhook target securely.
                Flags:
                  -source-platform (default: telegram)
                  -source-id       (required)
                  -url             (required)
                  -secret          (optional HMAC secret)
`
	fmt.Println(strings.TrimSpace(usage))
}
