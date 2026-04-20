# Telegram to Discord Bridge Bot

A Go bot that forwards media from Telegram chats to mapped Discord channels.

## Features

- Telegram media forwarding to Discord attachments
- Discord channel mapping via `!join <telegram_chat_id>`
- Caption keyword blocking via Telegram `/block <word_or_phrase>`
- Media-group (album) batching before Discord send
- SQLite persistence for chat-channel pairings and blocked words
- Retry + timeout handling for Telegram file downloads

## Commands

Telegram commands:

- `/id` or `/chatid`: Show the current Telegram chat ID
- `/block <word_or_phrase>`: Add a blocked word/phrase for this chat
- `/help`: Show command help

Discord commands:

- `!join <telegram_chat_id>`: Link the current Discord channel with a Telegram chat
- `!help`: Show command help

## Environment Variables

Required:

- `TELEGRAM_BOT_TOKEN`
- `DISCORD_BOT_TOKEN`

Optional:

- `MEDIA_QUEUE_SIZE` (default: `100`)

Notes:

- `.env` is optional. If present, it is loaded automatically.
- If `.env` is missing, the bot uses process-level environment variables.

## Run Locally

```bash
go test ./...
go run ./cmd/main.go
```

## Run With Docker

```bash
docker compose up --build -d
```

## Data Storage

The SQLite database file is `bot.db` and is mounted in Docker Compose so pairings and blocked words persist across restarts.
