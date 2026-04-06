# syntax=docker/dockerfile:1.7

# ---------- build ----------
FROM golang:1.26-bookworm AS build
WORKDIR /src

# Cache modules separately for better layer reuse.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download || true

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/ircat ./cmd/ircat

# ---------- runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/ircat /app/ircat
COPY docker/default-config.yaml /etc/ircat/config.yaml

EXPOSE 6667 6697 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/ircat"]
CMD ["--config", "/etc/ircat/config.yaml"]
