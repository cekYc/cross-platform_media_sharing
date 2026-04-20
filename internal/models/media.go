// internal/models/media.go
package models

type MediaEvent struct {
	Data         []byte // Dosya içeriği
	FileName     string // Dosya adı (örn: video.mp4)
	Caption      string // Mesaj altındaki metin
	SourceTGID   string // Mesajın geldiği Telegram Chat ID'si
	MediaGroupID string // Albüm tespiti için grup kimliği

}
