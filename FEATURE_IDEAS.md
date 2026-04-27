# Feature Ideas for tg-dc-bot

This document collects all feature and product ideas for the project in one place.

## 1) High-Impact Quick Wins

- [x] 1. Admin command set expansion
	- [x] Discord: !unlink, !status, !help, !blocklist, !unblock <word>, !clearblocks
	- [x] Telegram: /status, /blocklist, /unblock <word>, /clearblocks

- [x] 2. Role-based command authorization
	- [x] Only users with Manage Channel or specific roles can run bridge/admin commands.

- [x] 3. Better pairing model
	- [x] Support one Telegram chat to multiple Discord channels.
	- [x] Support optional per-channel filter sets.

- [x] 4. Safer media handling
	- [x] Hard file-size limits per media type.
	- [x] MIME/type allowlist to avoid unexpected file types.

- [x] 5. Duplicate prevention
	- [x] Compute file hash and skip repeated media within time window.

## 2) Reliability and Delivery Guarantees

- [x] 1. Persistent queue
	- [x] Store queued jobs in SQLite (or Redis) so restarts do not lose messages.

- [x] 2. Retry policy with backoff
	- [x] Exponential backoff on Discord send failures.
	- [x] Retry caps and clear failure reasons.

- [x] 3. Dead-letter queue
	- [x] Move permanently failed events to a dead-letter table.
	- [x] Add commands to inspect and replay failed events.

- [x] 4. Idempotent event processing
	- [x] Use event IDs to prevent double-send in reconnect scenarios.

- [x] 5. Better album coordination
	- [x] Replace fixed sleep with short collector window + inactivity timer.

## 3) Observability and Operations

- [x] 1. Health endpoints
	- [x] /healthz, /readyz

- [x] 2. Metrics
	- [x] Prometheus counters: received events, forwarded events, filtered events, failed events.
	- [x] Gauges: queue depth, retry depth.

- [x] 3. Structured logging
	- [x] JSON logs with correlation ID per event.

- [x] 4. Alerting
	- [x] Alert on high failure rate, high queue usage, or prolonged reconnect loops.

- [x] 5. Runtime config controls
	- [x] Adjustable limits/timeouts via env vars without code changes.

## 4) Moderation and Rule Engine

- [x] 1. Advanced filtering rules
	- [x] Word, phrase, regex, and sender-based rules.
	- [x] Include and exclude precedence.

- [x] 2. File rules
	- [x] Per-chat or per-target limits for type, size, duration.

- [x] 3. Time-based rules
	- [x] Quiet hours: queue content and deliver later.

- [x] 4. Spam controls
	- [x] Burst limits and repeated-caption detection.

- [ ] 5. Optional AI moderation layer (Skipped for now)
	- [ ] NSFW/spam scoring before forwarding.

## 5) Message Formatting and UX

- [x] 1. Rich caption templates
	- [x] Include sender name, source chat, timestamp, and media metadata.

- [x] 2. Localization
	- [x] Multi-language command responses (EN/TR), configurable default language.

- [x] 3. Better help and onboarding
	- [x] Guided setup flow with validation checks.

- [x] 4. Command discoverability
	- [x] Explicit /help sections per role and per platform.

- [x] 5. Reply mapping
	- [x] Optional reference linking so Discord replies can include source context.

## 6) Platform Expansion

- [x] 1. Bidirectional bridge (optional mode)
	- [x] Discord -> Telegram forwarding with permission checks.

- [x] 2. Additional targets
	- [x] Slack, Teams, Matrix, or generic webhook sink.

- [x] 3. Pluggable transport layer
	- [x] Keep Telegram/Discord adapters behind common interfaces.

## 7) Security Improvements

- [x] 1. Secret hygiene
- Support loading tokens from secret managers (via `_FILE` env variables).
- Never log token-like values (automatically masked via `security.MaskSecrets`).

- [x] 2. Access control hardening
- Restrict link/unlink actions to trusted users (via `TRUSTED_USER_IDS` env variables).

- [x] 3. Audit trail
- Record who changed pairings/filters and when in `audit_log` table. Available via `!auditlog` command.

- [x] 4. Safer defaults
- Conservative size/type limits enabled by default (50MB max, safe mime types).

- [x] 5. Abuse safeguards
- Rate limits per source chat and per destination channel (adjustable sliding window).

## 8) Data and Schema Improvements

- [x] 1. Normalize blocked words
- Move from comma-separated text to a separate table.

- [x] 2. Versioned migrations
- Introduce migration files and a schema version table.

- [x] 3. Event history
- Optional lightweight event log for debug/replay.

- [x] 4. Retention policy
- Auto-clean old logs and dead-letter records.

## 9) Testing and Quality

- [x] 1. Unit tests
	- [x] Command parsing, rules, retries, queue behavior.

- [x] 2. Integration tests
	- [x] Telegram/Discord client mocks + DB lifecycle tests.

- [x] 3. Resilience tests
	- [x] Network failures, reconnect storms, and timeout behavior.

- [x] 4. Performance tests
	- [x] Album bursts, large file scenarios, queue pressure.

- [x] 5. CI pipeline
	- [x] go test, race detector, lint, and docker build checks.

## 10) Suggested Delivery Roadmap

### Phase 1 (1 week)
- Admin command expansion
- Role-based auth
- Better help text
- Basic metrics and /healthz

### Phase 2 (2-3 weeks)
- Persistent queue
- Retry + dead-letter flow
- Duplicate prevention
- Advanced filter rules

### Phase 3 (3-5 weeks)
- Web admin panel (optional)
- Bidirectional mode (optional)
- Multi-target adapters
- Full observability + alerts

## 11) Most Practical Next 5 Features

If implementation starts now, the best next 5 are:

1. Role-based authorization for commands.
2. /blocklist + /unblock + /clearblocks command set.
3. Persistent queue and dead-letter table.
4. Prometheus metrics + /healthz endpoint.
5. Duplicate media detection by hash.

## 12) Nice-to-Have Stretch Ideas

1. Daily digest mode (forward summaries instead of instant forwarding).
2. Smart routing based on caption tags (for example: #news, #media).
3. Rule simulation mode (dry-run filters without blocking).
4. Per-chat dashboards with forwarding statistics.
5. Export/import pairing and rules in JSON.
