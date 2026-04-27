package security

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rate limit configuration loaded from environment at init time.
var (
	SourceRateLimitMax    int
	SourceRateLimitWindow time.Duration
	DestRateLimitMax      int
	DestRateLimitWindow   time.Duration
)

func init() {
	SourceRateLimitMax = parseIntEnvDefault("RATE_LIMIT_SOURCE_MAX", 30)
	SourceRateLimitWindow = time.Duration(parseIntEnvDefault("RATE_LIMIT_SOURCE_WINDOW_SECONDS", 60)) * time.Second
	DestRateLimitMax = parseIntEnvDefault("RATE_LIMIT_DEST_MAX", 60)
	DestRateLimitWindow = time.Duration(parseIntEnvDefault("RATE_LIMIT_DEST_WINDOW_SECONDS", 60)) * time.Second
}

// ---- sliding-window counters ----

type rateBucket struct {
	count    int
	windowAt time.Time
}

var (
	sourceMu      sync.Mutex
	sourceBuckets = make(map[string]*rateBucket)

	destMu      sync.Mutex
	destBuckets = make(map[string]*rateBucket)
)

// CheckSourceRateLimit returns true if the source is within allowed limits.
// sourceKey should be "platform:sourceID", e.g. "telegram:-100123".
func CheckSourceRateLimit(sourceKey string) bool {
	if SourceRateLimitMax <= 0 {
		return true // disabled
	}
	return checkLimit(&sourceMu, sourceBuckets, sourceKey, SourceRateLimitMax, SourceRateLimitWindow)
}

// CheckDestinationRateLimit returns true if the destination is within allowed limits.
// destKey should be "platform:destID", e.g. "discord:123456789".
func CheckDestinationRateLimit(destKey string) bool {
	if DestRateLimitMax <= 0 {
		return true // disabled
	}
	return checkLimit(&destMu, destBuckets, destKey, DestRateLimitMax, DestRateLimitWindow)
}

func checkLimit(mu *sync.Mutex, buckets map[string]*rateBucket, key string, max int, window time.Duration) bool {
	mu.Lock()
	defer mu.Unlock()

	now := time.Now()
	bucket, exists := buckets[key]
	if !exists || now.Sub(bucket.windowAt) > window {
		buckets[key] = &rateBucket{count: 1, windowAt: now}
		return true
	}

	if bucket.count >= max {
		return false
	}

	bucket.count++
	return true
}

// ResetRateLimits clears all rate limit state (used in tests).
func ResetRateLimits() {
	sourceMu.Lock()
	sourceBuckets = make(map[string]*rateBucket)
	sourceMu.Unlock()

	destMu.Lock()
	destBuckets = make(map[string]*rateBucket)
	destMu.Unlock()
}

func parseIntEnvDefault(name string, defaultVal int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(raw)
	if err != nil || val < 0 {
		return defaultVal
	}
	return val
}
