package observability

import (
	"strings"
	"testing"
	"time"
)

func TestIsReconnectLike(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   bool
	}{
		{name: "gateway hint", reason: "gateway reconnect failed", want: true},
		{name: "timeout hint", reason: "network timeout while sending", want: true},
		{name: "plain db error", reason: "constraint violation", want: false},
		{name: "empty", reason: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isReconnectLike(tc.reason)
			if got != tc.want {
				t.Fatalf("isReconnectLike(%q) = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}

func TestRenderPrometheusMetrics(t *testing.T) {
	originalStart := startedAt
	startedAt = time.Now().Add(-15 * time.Second)
	t.Cleanup(func() {
		startedAt = originalStart
	})

	snapshot := countersSnapshot{
		Received:        10,
		Forwarded:       8,
		Filtered:        1,
		Failed:          2,
		Retries:         3,
		DeadLetters:     1,
		ReconnectStreak: 4,
	}

	metrics := renderPrometheusMetrics(snapshot, 7, 2, 5)

	expectedLines := []string{
		"bridge_received_events_total 10",
		"bridge_forwarded_events_total 8",
		"bridge_filtered_events_total 1",
		"bridge_failed_events_total 2",
		"bridge_retries_scheduled_total 3",
		"bridge_dead_letter_events_total 1",
		"bridge_queue_depth 7",
		"bridge_retry_depth 2",
		"bridge_reconnect_streak 4",
	}

	for _, line := range expectedLines {
		if !strings.Contains(metrics, line) {
			t.Fatalf("metrics output missing line: %s", line)
		}
	}
}
