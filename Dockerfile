FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o jelly-cms ./cmd/server

FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/jelly-cms .
# web directory will be copied if it exists, otherwise we might need to handle it
COPY --from=builder /app/web ./web
# data directory will be mounted as a volume

EXPOSE 8080

CMD ["./jelly-cms"]
