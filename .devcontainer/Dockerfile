FROM mcr.microsoft.com/devcontainers/go:1-1.26-bookworm

# Extra tools commonly needed while developing ircat.
RUN go install honnef.co/go/tools/cmd/staticcheck@latest \
 && go install github.com/air-verse/air@latest \
 && apt-get update \
 && apt-get install -y --no-install-recommends sqlite3 postgresql-client \
 && rm -rf /var/lib/apt/lists/*
