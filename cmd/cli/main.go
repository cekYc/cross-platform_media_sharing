package main

import (
	"encoding/json"
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

	case "export-pairings":
		exportCmd := flag.NewFlagSet("export-pairings", flag.ExitOnError)
		outPath := exportCmd.String("out", "", "Output file path (defaults to stdout)")
		limit := exportCmd.Int("limit", 500, "Maximum pairings to export")
		includeSecrets := exportCmd.Bool("include-secrets", false, "Include webhook secrets in export")
		exportCmd.Parse(os.Args[2:])

		items, err := database.ExportPairings(*limit, *includeSecrets)
		if err != nil {
			log.Fatalf("Export failed: %v", err)
		}

		payload, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			log.Fatalf("Failed to encode export: %v", err)
		}

		if strings.TrimSpace(*outPath) == "" {
			fmt.Println(string(payload))
			return
		}
		if err := os.WriteFile(*outPath, payload, 0644); err != nil {
			log.Fatalf("Failed to write export file: %v", err)
		}
		fmt.Printf("Exported %d pairings to %s\n", len(items), *outPath)

	case "import-pairings":
		importCmd := flag.NewFlagSet("import-pairings", flag.ExitOnError)
		filePath := importCmd.String("file", "", "Path to JSON export file")
		includeSecrets := importCmd.Bool("include-secrets", false, "Apply webhook secrets from the import")
		replaceBlocked := importCmd.Bool("replace-blocked", true, "Replace blocked words with imported values")
		importCmd.Parse(os.Args[2:])

		if strings.TrimSpace(*filePath) == "" {
			fmt.Println("Error: -file is required")
			importCmd.PrintDefaults()
			return
		}

		data, err := os.ReadFile(*filePath)
		if err != nil {
			log.Fatalf("Failed to read import file: %v", err)
		}

		var items []database.PairingExport
		if err := json.Unmarshal(data, &items); err != nil {
			log.Fatalf("Invalid import JSON: %v", err)
		}

		count, err := database.ImportPairings(items, *includeSecrets, *replaceBlocked)
		if err != nil {
			log.Fatalf("Import failed: %v", err)
		}

		fmt.Printf("Imported %d pairings\n", count)

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
	export-pairings Export pairings and rule configs to JSON.
								Flags:
									-out             (optional output file)
									-limit           (default: 500)
									-include-secrets (include webhook secrets)
	import-pairings Import pairings and rule configs from JSON.
								Flags:
									-file            (required JSON file)
									-include-secrets (apply webhook secrets)
									-replace-blocked (default: true)
`
	fmt.Println(strings.TrimSpace(usage))
}
