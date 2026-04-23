package security

import (
	"testing"
	"time"
)

func TestCheckSourceRateLimit_AllowsWithinLimit(t *testing.T) {
	ResetRateLimits()
	oldMax := SourceRateLimitMax
	oldWindow := SourceRateLimitWindow
	defer func() {
		SourceRateLimitMax = oldMax
		SourceRateLimitWindow = oldWindow
	}()

	SourceRateLimitMax = 3
	SourceRateLimitWindow = 10 * time.Second

	key := "telegram:-100"

	for i := 0; i < 3; i++ {
		if !CheckSourceRateLimit(key) {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}

	if CheckSourceRateLimit(key) {
		t.Fatal("expected 4th request to be rate-limited")
	}
}

func TestCheckDestinationRateLimit_AllowsWithinLimit(t *testing.T) {
	ResetRateLimits()
	oldMax := DestRateLimitMax
	oldWindow := DestRateLimitWindow
	defer func() {
		DestRateLimitMax = oldMax
		DestRateLimitWindow = oldWindow
	}()

	DestRateLimitMax = 2
	DestRateLimitWindow = 10 * time.Second

	key := "discord:12345"

	if !CheckDestinationRateLimit(key) {
		t.Fatal("expected first request to be allowed")
	}
	if !CheckDestinationRateLimit(key) {
		t.Fatal("expected second request to be allowed")
	}
	if CheckDestinationRateLimit(key) {
		t.Fatal("expected third request to be rate-limited")
	}
}

func TestCheckSourceRateLimit_DisabledWhenZero(t *testing.T) {
	ResetRateLimits()
	oldMax := SourceRateLimitMax
	defer func() { SourceRateLimitMax = oldMax }()

	SourceRateLimitMax = 0

	for i := 0; i < 100; i++ {
		if !CheckSourceRateLimit("any-key") {
			t.Fatal("expected all requests to be allowed when rate limit is disabled")
		}
	}
}

func TestCheckSourceRateLimit_WindowReset(t *testing.T) {
	ResetRateLimits()
	oldMax := SourceRateLimitMax
	oldWindow := SourceRateLimitWindow
	defer func() {
		SourceRateLimitMax = oldMax
		SourceRateLimitWindow = oldWindow
	}()

	SourceRateLimitMax = 1
	SourceRateLimitWindow = 50 * time.Millisecond

	key := "telegram:-999"

	if !CheckSourceRateLimit(key) {
		t.Fatal("expected first request to be allowed")
	}
	if CheckSourceRateLimit(key) {
		t.Fatal("expected second request to be rate-limited")
	}

	time.Sleep(60 * time.Millisecond)

	if !CheckSourceRateLimit(key) {
		t.Fatal("expected request after window reset to be allowed")
	}
}
