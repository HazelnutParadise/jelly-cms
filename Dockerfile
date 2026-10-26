FROM golang:1.27-alpine AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILT_AT=

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags "-X github.com/HazelnutParadise/jelly-cms/internal/version.Version=${VERSION} -X github.com/HazelnutParadise/jelly-cms/internal/version.Commit=${COMMIT} -X github.com/HazelnutParadise/jelly-cms/internal/version.BuiltAt=${BUILT_AT}" -o jelly-cms ./cmd/server

FROM alpine:latest

RUN apk add --no-cache tzdata \
 && addgroup -S jelly \
 && adduser -S -G jelly jelly

WORKDIR /app

COPY --from=builder /app/jelly-cms .
# web directory will be copied if it exists, otherwise we might need to handle it
COPY --from=builder /app/web ./web
# data directory will be mounted as a volume
RUN mkdir -p /app/data && chown jelly:jelly /app/data

USER jelly

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

CMD ["./jelly-cms"]
