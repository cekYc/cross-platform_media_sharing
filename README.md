# Telegram to Discord Bridge Bot

A Go bot that forwards media from Telegram chats to mapped Discord channels.

## Features

- **Pluggable Transport Layer**: Generic producer/consumer interfaces that support multiple platforms.
- **Bidirectional Bridge**: Forward media from Telegram to Discord, OR from Discord to Telegram.
- **Webhook Sink Integration**: Send events securely to generic JSON Webhooks with HMAC-SHA256 signature verification.
- **Memory-Efficient Streaming**: Large attachments stream directly to platforms (e.g. via `io.Pipe`) without loading files fully into RAM.
- **CLI Management Tool**: Securely manage pairings and Webhook secrets via a built-in command line interface without exposing secrets in chat.
- Multi-link mapping: one source can forward to multiple targets.
- Per-channel block lists and admin command controls.
- **Advanced Moderation Engine**: Per-channel JSON configuration for regex filtering, sender blocks, and specific file size/MIME rules.
- **Spam Controls & Time Rules**: Built-in burst limiting and quiet hours (queue content during quiet hours, deliver later).
- **Rich Message Formatting**: Configurable templates (e.g. `{sender}`, `{caption}`) and automatic Reply mapping blockquotes to preserve context.
- **Localization & Onboarding**: Multi-language support (EN/TR) for admin commands and guided `/start` onboarding in Telegram.
- Persistent SQLite-backed delivery queue (survives restarts).
- Delivery retry with exponential backoff and dead-letter queue.
- Idempotent event processing via deterministic event IDs.
- Health and readiness endpoints (`/healthz`, `/readyz`).
- Prometheus-style metrics endpoint (`/metrics`).
- Structured JSON logs with per-event correlation IDs.

## Commands

Telegram commands:

- `/id` or `/chatid`: Show the current Telegram chat ID
- `/status`: Show linked Discord channels
- `/block <word_or_phrase>`: Add blocked text for all linked channels
- `/block <discord_channel_id> <word_or_phrase>`: Add blocked text for one channel
- `/blocklist [discord_channel_id]`: Show block list(s)
- `/unblock <word_or_phrase>`: Remove blocked text from all linked channels
- `/unblock <discord_channel_id> <word_or_phrase>`: Remove from one channel
- `/clearblocks [discord_channel_id]`: Clear blocked text
- `/setrule <discord_channel_id> <json>`: Set advanced rules (JSON format)
- `/help`: Show command help

Discord commands:

- `!join <telegram_chat_id>`: Link the current Discord channel with a Telegram chat
- `!unlink <telegram_chat_id>`: Remove a Telegram link from this channel
- `!status [telegram_chat_id]`: Show linked status and block list stats
- `!blocklist [telegram_chat_id]`: Show blocked words
- `!unblock <word_or_phrase>` or `!unblock <telegram_chat_id> <word_or_phrase>`: Remove blocked text
- `!clearblocks [telegram_chat_id]`: Clear blocked words
- `!deadletters [limit]`: Inspect failed deliveries for this channel
- `!replaydead <dead_letter_id>`: Replay a dead-letter item
- `!setrule <telegram_chat_id> <json>`: Set advanced rules (JSON format)
- `!help`: Show command help

## Environment Variables

Required:

- `TELEGRAM_BOT_TOKEN`
- `DISCORD_BOT_TOKEN`

Optional:

- `DISCORD_ADMIN_ROLE_IDS` (comma-separated role IDs with bridge admin access)
- `DUPLICATE_WINDOW_SECONDS` (default: `600`)
- `QUEUE_POLL_MILLISECONDS` (default: `250`)
- `QUEUE_PROCESSING_LEASE_SECONDS` (default: `30`)
- `DELIVERY_MAX_RETRIES` (default: `5`)
- `DELIVERY_RETRY_BASE_SECONDS` (default: `2`)
- `OBSERVABILITY_HTTP_ENABLED` (default: `true`)
- `OBSERVABILITY_HTTP_ADDR` (default: `:8081`)
- `READY_CONSUMER_STALE_SECONDS` (default: `30`)
- `ALERT_EVALUATION_INTERVAL_SECONDS` (default: `30`)
- `ALERT_FAILURE_RATE_THRESHOLD` (default: `0.25`)
- `ALERT_FAILURE_MIN_SAMPLE_SIZE` (default: `10`)
- `ALERT_QUEUE_DEPTH_THRESHOLD` (default: `500`)
- `ALERT_RETRY_DEPTH_THRESHOLD` (default: `100`)
- `ALERT_RECONNECT_STREAK_THRESHOLD` (default: `5`)

Notes:

- `.env` is optional. If present, it is loaded automatically.
- If `.env` is missing, the bot uses process-level environment variables.

## Run Locally

```bash
go test ./...
go run ./cmd/main.go
```

Observability endpoints (default bind `:8081`):

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`

## Run With Docker

```bash
docker compose up --build -d
```

## Data Storage

The SQLite database file is `bot.db` and is mounted in Docker Compose so state persists across restarts.

Stored tables include:

- Pairings and block lists
- Pending delivery queue
- Processed event IDs (idempotency)
- Dead-letter deliveries
