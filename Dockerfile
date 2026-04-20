# 1. Aşama: Derleme (Builder)
FROM golang:1.26.2-alpine AS builder
WORKDIR /app

# Sadece bağımlılıkları kopyala ve indir (Cache optimizasyonu)
COPY go.mod go.sum ./
RUN go mod download

# Tüm kodları kopyala
COPY . .

# Saf Go ve statik bir binary oluştur (CGO_ENABLED=0 çok önemlidir)
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o bot ./cmd/main.go

# 2. Aşama: Çalıştırma (Çok hafif bir imaj oluşturuyoruz)
FROM alpine:latest
WORKDIR /app

# HTTPS is required for Telegram/Discord API calls.
RUN apk add --no-cache ca-certificates

# Derlenen binary dosyasını ilk aşamadan buraya al
COPY --from=builder /app/bot .

# Botu başlat
CMD ["./bot"]