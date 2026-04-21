package observability

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"tg-discord-bot/internal/database"
	"time"
)

type config struct {
	httpEnabled                   bool
	httpAddr                      string
	alertEvaluationInterval       time.Duration
	alertFailureRateThreshold     float64
	alertFailureMinSampleSize     int64
	alertQueueDepthThreshold      int
	alertRetryDepthThreshold      int
	alertReconnectStreakThreshold int64
	readyConsumerMaxStaleness     time.Duration
}

type countersSnapshot struct {
	Received        int64
	Forwarded       int64
	Filtered        int64
	Failed          int64
	Retries         int64
	DeadLetters     int64
	ReconnectStreak int64
}

var (
	startOnce sync.Once
	cfg       config
	startedAt = time.Now()

	receivedEventsTotal   int64
	forwardedEventsTotal  int64
	filteredEventsTotal   int64
	failedEventsTotal     int64
	retryScheduledTotal   int64
	deadLetterEventsTotal int64
	reconnectStreak       int64

	consumerHeartbeatUnix int64
)

func Start() {
	startOnce.Do(func() {
		cfg = loadConfig()

		Log("info", "observability initialized", map[string]interface{}{
			"http_enabled": cfg.httpEnabled,
			"http_addr":    cfg.httpAddr,
		})

		if cfg.httpEnabled {
			go runHTTPServer()
		}

		go runAlertLoop()
	})
}

func MarkConsumerHeartbeat() {
	atomic.StoreInt64(&consumerHeartbeatUnix, time.Now().Unix())
}

func RegisterEventEnqueued() {
	atomic.AddInt64(&receivedEventsTotal, 1)
}

func RegisterEventsForwarded(count int64) {
	if count <= 0 {
		return
	}

	atomic.AddInt64(&forwardedEventsTotal, count)
	atomic.StoreInt64(&reconnectStreak, 0)
}

func RegisterEventFiltered() {
	atomic.AddInt64(&filteredEventsTotal, 1)
}

func RegisterDeliveryFailure(reason string) {
	atomic.AddInt64(&failedEventsTotal, 1)
	if isReconnectLike(reason) {
		atomic.AddInt64(&reconnectStreak, 1)
	}
}

func RegisterRetryScheduled() {
	atomic.AddInt64(&retryScheduledTotal, 1)
}

func RegisterDeadLetterMoved() {
	atomic.AddInt64(&deadLetterEventsTotal, 1)
}

func Log(level, message string, fields map[string]interface{}) {
	entry := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"level":     strings.ToLower(strings.TrimSpace(level)),
		"message":   message,
	}

	for key, value := range fields {
		if strings.TrimSpace(key) == "" {
			continue
		}
		entry[key] = value
	}

	payload, err := json.Marshal(entry)
	if err != nil {
		log.Printf("[LOG-ERROR] failed to marshal structured log entry: %v", err)
		return
	}

	log.Print(string(payload))
}

func LogEvent(level, message, correlationID string, fields map[string]interface{}) {
	if fields == nil {
		fields = map[string]interface{}{}
	}
	if strings.TrimSpace(correlationID) != "" {
		fields["correlation_id"] = strings.TrimSpace(correlationID)
	}

	Log(level, message, fields)
}

func runHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/readyz", readyHandler)
	mux.HandleFunc("/metrics", metricsHandler)

	server := &http.Server{
		Addr:              cfg.httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		Log("error", "observability HTTP server failed", map[string]interface{}{"error": err.Error()})
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "ok",
		"uptime_seconds": int64(time.Since(startedAt).Seconds()),
	})
}

func readyHandler(w http.ResponseWriter, _ *http.Request) {
	statusCode := http.StatusOK
	ready := true

	dbStatus := "ok"
	if database.DB == nil {
		ready = false
		dbStatus = "db_not_initialized"
	} else if err := database.DB.Ping(); err != nil {
		ready = false
		dbStatus = "db_unreachable"
	}

	consumerStatus := "ok"
	lastHeartbeat := atomic.LoadInt64(&consumerHeartbeatUnix)
	if lastHeartbeat == 0 {
		ready = false
		consumerStatus = "consumer_not_started"
	} else {
		staleness := time.Since(time.Unix(lastHeartbeat, 0))
		if staleness > cfg.readyConsumerMaxStaleness {
			ready = false
			consumerStatus = fmt.Sprintf("consumer_stale_%ds", int64(staleness.Seconds()))
		}
	}

	if !ready {
		statusCode = http.StatusServiceUnavailable
	}

	writeJSON(w, statusCode, map[string]interface{}{
		"ready": ready,
		"checks": map[string]string{
			"database": dbStatus,
			"consumer": consumerStatus,
		},
	})
}

func metricsHandler(w http.ResponseWriter, _ *http.Request) {
	snapshot := snapshotCounters()
	queueDepth, retryDepth, err := database.GetQueueStats()
	if err != nil {
		Log("warn", "failed to load queue stats for metrics", map[string]interface{}{"error": err.Error()})
		queueDepth = -1
		retryDepth = -1
	}

	consumerAge := consumerHeartbeatAgeSeconds()
	payload := renderPrometheusMetrics(snapshot, queueDepth, retryDepth, consumerAge)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(payload))
}

func runAlertLoop() {
	ticker := time.NewTicker(cfg.alertEvaluationInterval)
	defer ticker.Stop()

	previous := snapshotCounters()

	for range ticker.C {
		snapshot := snapshotCounters()
		queueDepth, retryDepth, err := database.GetQueueStats()
		if err != nil {
			Log("warn", "failed to evaluate queue-based alerts", map[string]interface{}{"error": err.Error()})
			previous = snapshot
			continue
		}

		forwardedDelta := snapshot.Forwarded - previous.Forwarded
		failedDelta := snapshot.Failed - previous.Failed
		deliverySamples := forwardedDelta + failedDelta
		if deliverySamples >= cfg.alertFailureMinSampleSize {
			failureRate := float64(failedDelta) / float64(deliverySamples)
			if failureRate >= cfg.alertFailureRateThreshold {
				Log("warn", "alert: high delivery failure rate", map[string]interface{}{
					"failure_rate":      failureRate,
					"failure_threshold": cfg.alertFailureRateThreshold,
					"delivery_samples":  deliverySamples,
					"failed_delta":      failedDelta,
					"forwarded_delta":   forwardedDelta,
				})
			}
		}

		if queueDepth >= cfg.alertQueueDepthThreshold {
			Log("warn", "alert: high queue depth", map[string]interface{}{
				"queue_depth": queueDepth,
				"threshold":   cfg.alertQueueDepthThreshold,
			})
		}

		if retryDepth >= cfg.alertRetryDepthThreshold {
			Log("warn", "alert: high retry depth", map[string]interface{}{
				"retry_depth": retryDepth,
				"threshold":   cfg.alertRetryDepthThreshold,
			})
		}

		if snapshot.ReconnectStreak >= cfg.alertReconnectStreakThreshold {
			Log("warn", "alert: prolonged reconnect loop suspected", map[string]interface{}{
				"reconnect_streak": snapshot.ReconnectStreak,
				"threshold":        cfg.alertReconnectStreakThreshold,
			})
		}

		previous = snapshot
	}
}

func snapshotCounters() countersSnapshot {
	return countersSnapshot{
		Received:        atomic.LoadInt64(&receivedEventsTotal),
		Forwarded:       atomic.LoadInt64(&forwardedEventsTotal),
		Filtered:        atomic.LoadInt64(&filteredEventsTotal),
		Failed:          atomic.LoadInt64(&failedEventsTotal),
		Retries:         atomic.LoadInt64(&retryScheduledTotal),
		DeadLetters:     atomic.LoadInt64(&deadLetterEventsTotal),
		ReconnectStreak: atomic.LoadInt64(&reconnectStreak),
	}
}

func consumerHeartbeatAgeSeconds() float64 {
	lastHeartbeat := atomic.LoadInt64(&consumerHeartbeatUnix)
	if lastHeartbeat == 0 {
		return -1
	}

	age := time.Since(time.Unix(lastHeartbeat, 0)).Seconds()
	if age < 0 {
		return 0
	}

	return age
}

func renderPrometheusMetrics(snapshot countersSnapshot, queueDepth, retryDepth int, consumerAge float64) string {
	lines := []string{
		"# HELP bridge_received_events_total Total number of events enqueued for delivery",
		"# TYPE bridge_received_events_total counter",
		fmt.Sprintf("bridge_received_events_total %d", snapshot.Received),
		"# HELP bridge_forwarded_events_total Total number of events forwarded successfully",
		"# TYPE bridge_forwarded_events_total counter",
		fmt.Sprintf("bridge_forwarded_events_total %d", snapshot.Forwarded),
		"# HELP bridge_filtered_events_total Total number of events filtered before delivery",
		"# TYPE bridge_filtered_events_total counter",
		fmt.Sprintf("bridge_filtered_events_total %d", snapshot.Filtered),
		"# HELP bridge_failed_events_total Total number of delivery failures",
		"# TYPE bridge_failed_events_total counter",
		fmt.Sprintf("bridge_failed_events_total %d", snapshot.Failed),
		"# HELP bridge_retries_scheduled_total Total number of retry attempts scheduled",
		"# TYPE bridge_retries_scheduled_total counter",
		fmt.Sprintf("bridge_retries_scheduled_total %d", snapshot.Retries),
		"# HELP bridge_dead_letter_events_total Total number of events moved to dead letter queue",
		"# TYPE bridge_dead_letter_events_total counter",
		fmt.Sprintf("bridge_dead_letter_events_total %d", snapshot.DeadLetters),
		"# HELP bridge_queue_depth Current pending event queue depth",
		"# TYPE bridge_queue_depth gauge",
		fmt.Sprintf("bridge_queue_depth %d", queueDepth),
		"# HELP bridge_retry_depth Current queue depth with retry_count > 0",
		"# TYPE bridge_retry_depth gauge",
		fmt.Sprintf("bridge_retry_depth %d", retryDepth),
		"# HELP bridge_reconnect_streak Current reconnect-like failure streak",
		"# TYPE bridge_reconnect_streak gauge",
		fmt.Sprintf("bridge_reconnect_streak %d", snapshot.ReconnectStreak),
		"# HELP bridge_consumer_heartbeat_age_seconds Seconds since the consumer loop heartbeat",
		"# TYPE bridge_consumer_heartbeat_age_seconds gauge",
		fmt.Sprintf("bridge_consumer_heartbeat_age_seconds %.0f", consumerAge),
		"# HELP bridge_uptime_seconds Process uptime in seconds",
		"# TYPE bridge_uptime_seconds gauge",
		fmt.Sprintf("bridge_uptime_seconds %d", int64(time.Since(startedAt).Seconds())),
	}

	return strings.Join(lines, "\n") + "\n"
}

func loadConfig() config {
	return config{
		httpEnabled:                   parseBoolEnv("OBSERVABILITY_HTTP_ENABLED", true),
		httpAddr:                      parseStringEnv("OBSERVABILITY_HTTP_ADDR", ":8081"),
		alertEvaluationInterval:       time.Duration(parseIntEnv("ALERT_EVALUATION_INTERVAL_SECONDS", 30)) * time.Second,
		alertFailureRateThreshold:     parseFloatEnv("ALERT_FAILURE_RATE_THRESHOLD", 0.25),
		alertFailureMinSampleSize:     int64(parseIntEnv("ALERT_FAILURE_MIN_SAMPLE_SIZE", 10)),
		alertQueueDepthThreshold:      parseIntEnv("ALERT_QUEUE_DEPTH_THRESHOLD", 500),
		alertRetryDepthThreshold:      parseIntEnv("ALERT_RETRY_DEPTH_THRESHOLD", 100),
		alertReconnectStreakThreshold: int64(parseIntEnv("ALERT_RECONNECT_STREAK_THRESHOLD", 5)),
		readyConsumerMaxStaleness:     time.Duration(parseIntEnv("READY_CONSUMER_STALE_SECONDS", 30)) * time.Second,
	}
}

func parseStringEnv(name, defaultValue string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return defaultValue
	}
	return value
}

func parseIntEnv(name string, defaultValue int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Printf("[WARN] invalid %s value (%q), using default: %d", name, value, defaultValue)
		return defaultValue
	}

	return parsed
}

func parseFloatEnv(name string, defaultValue float64) float64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 {
		log.Printf("[WARN] invalid %s value (%q), using default: %.4f", name, value, defaultValue)
		return defaultValue
	}

	return parsed
}

func parseBoolEnv(name string, defaultValue bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if value == "" {
		return defaultValue
	}

	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		log.Printf("[WARN] invalid %s value (%q), using default: %t", name, value, defaultValue)
		return defaultValue
	}
}

func isReconnectLike(reason string) bool {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	if normalized == "" {
		return false
	}

	hints := []string{
		"gateway",
		"websocket",
		"tls",
		"connection",
		"timeout",
		"eof",
		"reset by peer",
		"connection refused",
	}

	for _, hint := range hints {
		if strings.Contains(normalized, hint) {
			return true
		}
	}

	return false
}

func writeJSON(w http.ResponseWriter, statusCode int, payload map[string]interface{}) {
	response, err := json.Marshal(payload)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("{\"error\":\"failed to render json\"}"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write(response)
}
