# syntax=docker/dockerfile:1

# Build stage
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./

# Enable GOTOOLCHAIN to auto-download the required Go version
ENV GOTOOLCHAIN=auto
RUN go mod download

COPY . .

# Build arguments for cross-compilation
ARG TARGETOS
ARG TARGETARCH

# CGO_ENABLED=0 ensures a static binary
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-w -s" -o bin/caramba ./cmd/server

# Runtime stage
FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/bin/caramba /usr/local/bin/caramba

EXPOSE 8080

CMD ["caramba", "--config", "/etc/caramba/config.yml"]
