package adminpanel

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"tg-discord-bot/internal/database"
)

type panelConfig struct {
	enabled      bool
	addr         string
	token        string
	defaultLimit int
}

var startOnce sync.Once

func Start() {
	startOnce.Do(func() {
		cfg := loadConfig()
		if !cfg.enabled {
			return
		}
		if strings.TrimSpace(cfg.token) == "" {
			log.Println("[WARN] admin panel enabled but ADMIN_HTTP_TOKEN is empty; panel will not start")
			return
		}

		server := &http.Server{
			Addr:              cfg.addr,
			Handler:           newMux(cfg),
			ReadHeaderTimeout: 5 * time.Second,
		}

		go func() {
			log.Printf("[INFO] admin panel listening on %s", cfg.addr)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("[ERROR] admin panel server failed: %v", err)
			}
		}()
	})
}

func newMux(cfg panelConfig) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/admin/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
	})

	mux.HandleFunc("/admin/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(indexHTML))
	})

	mux.HandleFunc("/admin/api/summary", withTokenAuth(cfg.token, func(w http.ResponseWriter, _ *http.Request) {
		queueDepth, retryDepth, err := database.GetQueueStats()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}

		pairingCount, err := database.CountPairings()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}

		deadLetterCount, err := database.CountDeadLetters()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"pairings":     pairingCount,
			"queue_depth":  queueDepth,
			"retry_depth":  retryDepth,
			"dead_letters": deadLetterCount,
			"generated_at": time.Now().UTC().Format(time.RFC3339),
		})
	}))

	mux.HandleFunc("/admin/api/pairings", withTokenAuth(cfg.token, func(w http.ResponseWriter, r *http.Request) {
		limit := cfg.defaultLimit
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err == nil && parsed > 0 {
				if parsed > 500 {
					parsed = 500
				}
				limit = parsed
			}
		}

		pairings, err := database.ListAllPairings(limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}

		items := make([]map[string]interface{}, 0, len(pairings))
		for _, pairing := range pairings {
			items = append(items, map[string]interface{}{
				"source_platform":         pairing.SourcePlatform,
				"source_id":               pairing.SourceID,
				"target_platform":         pairing.TargetPlatform,
				"target_id":               pairing.TargetID,
				"blocked_word_count":      len(pairing.BlockedWords),
				"blocked_words":           pairing.BlockedWords,
				"language":                pairing.RuleConfig.Language,
				"ai_moderation_enabled":   pairing.RuleConfig.AIModerationEnabled,
				"ai_moderation_threshold": pairing.RuleConfig.AIModerationThreshold,
			})
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"items": items,
			"count": len(items),
		})
	}))

	return mux
}

func withTokenAuth(expectedToken string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthorized(r, expectedToken) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="admin"`)
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func isAuthorized(r *http.Request, expectedToken string) bool {
	token := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if token == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			token = strings.TrimSpace(auth[7:])
		}
	}
	return token != "" && token == strings.TrimSpace(expectedToken)
}

func loadConfig() panelConfig {
	return panelConfig{
		enabled:      parseBoolEnv("ADMIN_HTTP_ENABLED", false),
		addr:         parseStringEnv("ADMIN_HTTP_ADDR", ":8091"),
		token:        strings.TrimSpace(os.Getenv("ADMIN_HTTP_TOKEN")),
		defaultLimit: parseIntEnv("ADMIN_HTTP_DEFAULT_LIMIT", 100),
	}
}

func parseStringEnv(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func parseIntEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseBoolEnv(name string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func writeJSON(w http.ResponseWriter, status int, payload map[string]interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, "failed to render json")))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

const indexHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <title>Bridge Admin Panel</title>
  <style>
    body { font-family: "Segoe UI", Tahoma, sans-serif; margin: 24px; background: #f6f8fa; color: #1f2937; }
    h1 { margin: 0 0 8px; }
    .row { margin-bottom: 12px; }
    input, button { padding: 8px 10px; font-size: 14px; }
    button { cursor: pointer; }
    table { border-collapse: collapse; width: 100%; background: #fff; }
    th, td { border: 1px solid #d1d5db; padding: 8px; text-align: left; }
    th { background: #e5e7eb; }
    .card { background: #fff; border: 1px solid #d1d5db; padding: 12px; margin-bottom: 14px; }
    .muted { color: #6b7280; }
  </style>
</head>
<body>
  <h1>Bridge Admin Panel</h1>
  <p class="muted">Read-only operations panel for pairings and queue state.</p>

  <div class="row">
    <input id="token" type="password" placeholder="Admin token" style="width: 280px;" />
    <button onclick="refreshAll()">Refresh</button>
  </div>

  <div id="summary" class="card">No data loaded yet.</div>

  <table>
    <thead>
      <tr>
        <th>Source</th>
        <th>Target</th>
        <th>Blocked Words</th>
        <th>Language</th>
        <th>AI Moderation</th>
      </tr>
    </thead>
    <tbody id="pairings"></tbody>
  </table>

  <script>
    async function callApi(path) {
      const token = document.getElementById('token').value.trim();
      const res = await fetch(path, {
        headers: {
          'Authorization': 'Bearer ' + token
        }
      });
      if (!res.ok) {
        throw new Error('Request failed: ' + res.status);
      }
      return await res.json();
    }

    async function refreshAll() {
      try {
        const summary = await callApi('/admin/api/summary');
        document.getElementById('summary').innerText =
          'Pairings: ' + summary.pairings +
          ' | Queue depth: ' + summary.queue_depth +
          ' | Retry depth: ' + summary.retry_depth +
          ' | Dead letters: ' + summary.dead_letters +
          ' | Generated at: ' + summary.generated_at;

        const pairings = await callApi('/admin/api/pairings?limit=200');
        const tbody = document.getElementById('pairings');
        tbody.innerHTML = '';
        pairings.items.forEach(item => {
          const tr = document.createElement('tr');
          tr.innerHTML =
            '<td>' + item.source_platform + ':' + item.source_id + '</td>' +
            '<td>' + item.target_platform + ':' + item.target_id + '</td>' +
            '<td>' + item.blocked_word_count + '</td>' +
            '<td>' + (item.language || '-') + '</td>' +
            '<td>' + (item.ai_moderation_enabled ? 'enabled (' + (item.ai_moderation_threshold || 'default') + ')' : 'disabled') + '</td>';
          tbody.appendChild(tr);
        });
      } catch (err) {
        alert(err.message + ' (check token and panel availability)');
      }
    }
  </script>
</body>
</html>`
