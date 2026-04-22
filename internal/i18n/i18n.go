package i18n

import "strings"

var messages = map[string]map[string]string{
	"en": {
		"help_title":       "Available Commands:",
		"help_id":          "Show this Telegram chat ID",
		"help_status":      "Show linked Discord channels",
		"help_block":       "Add blocked text (all or specific channel)",
		"help_blocklist":   "Show blocked words",
		"help_unblock":     "Remove blocked text",
		"help_clearblocks": "Clear blocked words",
		"help_setrule":     "Set advanced rules (JSON format)",
		"help_unknown":     "Unknown command. Use /help",
		"welcome_step1":    "Welcome! I am a bridge bot.",
		"welcome_step2":    "1. Add me to your Discord server.\n2. In Discord, run: `!join %s`\n3. Enjoy seamless media forwarding!",
		"cmd_id":           "This chat ID is: `%s`\n\nIn Discord, run:\n`!join %s`",
	},
	"tr": {
		"help_title":       "Mevcut Komutlar:",
		"help_id":          "Bu Telegram sohbet ID'sini göster",
		"help_status":      "Bağlı Discord kanallarını göster",
		"help_block":       "Yasaklı kelime ekle (tümü veya belirli kanal)",
		"help_blocklist":   "Yasaklı kelimeleri göster",
		"help_unblock":     "Yasaklı kelimeyi kaldır",
		"help_clearblocks": "Yasaklı kelimeleri temizle",
		"help_setrule":     "Gelişmiş kuralları ayarla (JSON formatı)",
		"help_unknown":     "Bilinmeyen komut. /help kullanın.",
		"welcome_step1":    "Hoş geldin! Ben bir köprü botuyum.",
		"welcome_step2":    "1. Beni Discord sunucuna ekle.\n2. Discord kanalında şunu yaz: `!join %s`\n3. Medyalar otomatik aktarılsın!",
		"cmd_id":           "Bu sohbetin ID'si: `%s`\n\nDiscord'da şunu yazın:\n`!join %s`",
	},
}

// Get returns the localized string for a given key. Falls back to english.
func Get(lang string, key string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		lang = "en"
	}
	
	dict, exists := messages[lang]
	if !exists {
		dict = messages["en"]
	}

	val, exists := dict[key]
	if !exists {
		// Fallback to english if missing in target lang
		val, exists = messages["en"][key]
		if !exists {
			return key // return key itself if totally missing
		}
	}
	return val
}
