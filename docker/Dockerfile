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

# Pre-create the runtime data + log directories with the
# distroless `nonroot` uid/gid (65532) so the runtime stage
# below has them owned correctly. The runtime image has no
# shell, so we cannot RUN mkdir directly inside it — we
# create them here and COPY them across.
#
# Why this matters: when docker compose mounts a *named*
# volume into one of these paths for the first time, the
# daemon copies the content + ownership of the image's
# directory into the volume. Without this step the image
# has no content at /var/lib/ircat or /var/log/ircat, the
# volume comes up empty and root-owned, and the nonroot
# process inside the container hits EACCES on the first
# write. With this step the volume inherits the
# 65532:65532 ownership we pre-stamped.
RUN install -d -o 65532 -g 65532 \
      /chown/var/lib/ircat \
      /chown/var/log/ircat \
      /chown/etc/ircat

# ---------- runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/ircat /app/ircat
COPY docker/default-config.yaml /etc/ircat/config.yaml

# Pre-stamped runtime directories with the right ownership.
# These end up under /var/{lib,log}/ircat and /etc/ircat
# inside the runtime image; on first named-volume mount they
# carry the 65532:65532 uid:gid into the volume.
COPY --from=build --chown=65532:65532 /chown/var/lib/ircat /var/lib/ircat
COPY --from=build --chown=65532:65532 /chown/var/log/ircat /var/log/ircat

EXPOSE 6667 6697 7000 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/ircat"]
CMD ["--config", "/etc/ircat/config.yaml"]
