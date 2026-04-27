package models

// RuleConfig defines the advanced rules per Discord channel pairing
type RuleConfig struct {
	// 1. Advanced filtering rules
	RegexFilters   []string `json:"regex_filters,omitempty"`
	BlockedSenders []string `json:"blocked_senders,omitempty"`
	// Include and exclude precedence (If IncludeWords is not empty, ONLY these are allowed, unless they hit Exclude/Blocked)
	IncludeWords []string `json:"include_words,omitempty"`

	// 2. File rules
	AllowedMimeTypes []string `json:"allowed_mime_types,omitempty"` // Override global
	MaxFileSizeMB    int      `json:"max_file_size_mb,omitempty"`   // Override global

	// 3. Time-based rules (Quiet hours)
	QuietHoursStart string `json:"quiet_hours_start,omitempty"` // Format "HH:MM" in UTC
	QuietHoursEnd   string `json:"quiet_hours_end,omitempty"`   // Format "HH:MM" in UTC

	// 4. Spam controls
	BurstLimit  int `json:"burst_limit,omitempty"`  // Messages allowed per BurstWindow
	BurstWindow int `json:"burst_window,omitempty"` // In seconds

	// 5. Message Formatting and UX
	CaptionTemplate string `json:"caption_template,omitempty"` // e.g. "From {sender}:\n{caption}"
	Language        string `json:"language,omitempty"`         // "en" or "tr"

	// 6. Optional AI moderation
	AIModerationEnabled   bool    `json:"ai_moderation_enabled,omitempty"`
	AIModerationThreshold float64 `json:"ai_moderation_threshold,omitempty"`
}
