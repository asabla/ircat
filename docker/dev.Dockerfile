# Dev image: has the Go toolchain and `air` for hot reload.
# Expected to be used via docker-compose.dev.yml with the source tree bind-mounted.
FROM golang:1.26-bookworm

WORKDIR /src
RUN go install github.com/air-verse/air@latest \
 && apt-get update \
 && apt-get install -y --no-install-recommends sqlite3 \
 && rm -rf /var/lib/apt/lists/*

EXPOSE 6667 6697 8080
CMD ["air", "-c", ".air.toml"]
