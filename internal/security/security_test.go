package security

import (
	"os"
	"path/filepath"
	"testing"

	"tg-discord-bot/internal/models"
)

func TestMaskSecrets_TelegramToken(t *testing.T) {
	token := "123456789:ABCDEFghijklMNOpqrstuvwxyz12345678"
	result := MaskSecrets("token is " + token + " end")
	if result == "token is "+token+" end" {
		t.Fatalf("expected Telegram token to be masked, got: %s", result)
	}
	if result != "token is ***REDACTED*** end" {
		t.Fatalf("unexpected mask result: %s", result)
	}
}

func TestMaskSecrets_Empty(t *testing.T) {
	if got := MaskSecrets(""); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestMaskSecrets_NoSecrets(t *testing.T) {
	input := "hello world, no secrets here"
	if got := MaskSecrets(input); got != input {
		t.Fatalf("expected no change, got %q", got)
	}
}

func TestMaskSecrets_LongHex(t *testing.T) {
	hex64 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	result := MaskSecrets("key=" + hex64)
	if result != "key=***REDACTED***" {
		t.Fatalf("expected hex secret to be masked, got: %s", result)
	}
}

func TestLoadSecret_PlainEnv(t *testing.T) {
	t.Setenv("TEST_SECRET", "my-plain-secret")

	got := LoadSecret("TEST_SECRET")
	if got != "my-plain-secret" {
		t.Fatalf("expected %q, got %q", "my-plain-secret", got)
	}
}

func TestLoadSecret_FileEnv(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("  file-loaded-secret  \n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TEST_SECRET2_FILE", secretFile)
	t.Setenv("TEST_SECRET2", "should-not-use-this")

	got := LoadSecret("TEST_SECRET2")
	if got != "file-loaded-secret" {
		t.Fatalf("expected %q, got %q", "file-loaded-secret", got)
	}
}

func TestLoadSecret_MissingFile(t *testing.T) {
	t.Setenv("TEST_SECRET3_FILE", "/nonexistent/path/secret.txt")
	t.Setenv("TEST_SECRET3", "fallback-value")

	got := LoadSecret("TEST_SECRET3")
	if got != "fallback-value" {
		t.Fatalf("expected %q, got %q", "fallback-value", got)
	}
}

func TestApplyFileRuleDefaults(t *testing.T) {
	empty := models.RuleConfig{}
	result := ApplyFileRuleDefaults(empty)

	if result.MaxFileSizeMB != DefaultMaxFileSizeMB {
		t.Fatalf("expected MaxFileSizeMB %d, got %d", DefaultMaxFileSizeMB, result.MaxFileSizeMB)
	}

	if len(result.AllowedMimeTypes) == 0 {
		t.Fatal("expected default AllowedMimeTypes to be set")
	}
}

func TestApplyFileRuleDefaults_ExplicitOverride(t *testing.T) {
	custom := models.RuleConfig{
		MaxFileSizeMB:    100,
		AllowedMimeTypes: []string{"image/png"},
	}
	result := ApplyFileRuleDefaults(custom)

	if result.MaxFileSizeMB != 100 {
		t.Fatalf("expected MaxFileSizeMB 100, got %d", result.MaxFileSizeMB)
	}

	if len(result.AllowedMimeTypes) != 1 || result.AllowedMimeTypes[0] != "image/png" {
		t.Fatalf("expected custom AllowedMimeTypes, got %v", result.AllowedMimeTypes)
	}
}
