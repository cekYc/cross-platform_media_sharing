package transport

import (
	"context"
	"fmt"
	"tg-discord-bot/internal/models"
)

type Consumer interface {
	PlatformID() string
	Send(ctx context.Context, event models.MediaEvent) error
}

type Producer interface {
	Start()
}

var consumers = make(map[string]Consumer)

func RegisterConsumer(c Consumer) {
	consumers[c.PlatformID()] = c
}

func GetConsumer(platformID string) (Consumer, error) {
	if c, ok := consumers[platformID]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("consumer not found for platform: %s", platformID)
}

// Ensure all consumers are registered at startup.
func Init() {
	// Adapters will register themselves via init() or manual registration in main.go
}
