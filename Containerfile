# Build stage
FROM registry.access.redhat.com/ubi9/go-toolset:latest AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o leak-prevention-server ./cmd/server

# Runtime stage
FROM registry.access.redhat.com/ubi9-minimal:latest
COPY --from=builder /app/leak-prevention-server /usr/local/bin/
COPY watchlist.db /data/watchlist.db
EXPOSE 8642
VOLUME /data/allowlist
USER 1001
CMD ["leak-prevention-server", "--watchlist", "/data/watchlist.db", "--allowlist-dir", "/data/allowlist", "--port", "8642"]
