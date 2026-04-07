#!/usr/bin/env sh
# install.sh — bootstrap an ircat install from a tagged GitHub release.
#
# Downloads the latest (or pinned) tar.gz from
# https://github.com/asabla/ircat/releases, extracts the binary,
# and optionally drops a sample compose file alongside it.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/asabla/ircat/main/scripts/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/asabla/ircat/main/scripts/install.sh | sh -s -- v1.1.0
#   IRCAT_PREFIX=/opt/ircat ./scripts/install.sh
#
# Environment variables:
#   IRCAT_VERSION   release tag to install (default: latest stable)
#   IRCAT_PREFIX    install root for the binary (default: /usr/local/bin)
#   IRCAT_OS        override the detected OS (linux, darwin)
#   IRCAT_ARCH      override the detected CPU arch (amd64, arm64)
#   IRCAT_VERIFY    if "1", verify checksums.txt against the release sig
#                   via cosign — requires cosign in PATH
#
# The script is intentionally POSIX sh so it runs on minimal
# busybox systems where bash is unavailable.

set -eu

REPO=asabla/ircat
PREFIX=${IRCAT_PREFIX:-/usr/local/bin}
VERSION=${1:-${IRCAT_VERSION:-}}

# ---- helpers ----------------------------------------------------

err() { printf 'install: %s\n' "$*" >&2; exit 1; }
log() { printf 'install: %s\n' "$*" >&2; }

# Resolve "latest" to the most recent stable tag via the GitHub API.
# We avoid jq and parse the tag_name field with sed so the script
# stays dependency-free on stripped-down hosts.
resolve_latest() {
  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' |
    head -n 1
}

# Detect the host OS / CPU using uname -s and uname -m.
detect_os() {
  case "$(uname -s)" in
    Linux) printf 'linux' ;;
    Darwin) printf 'darwin' ;;
    *) err "unsupported OS: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *) err "unsupported arch: $(uname -m)" ;;
  esac
}

# ---- main -------------------------------------------------------

OS=${IRCAT_OS:-$(detect_os)}
ARCH=${IRCAT_ARCH:-$(detect_arch)}

if [ -z "${VERSION}" ]; then
  log 'resolving latest release tag from github'
  VERSION=$(resolve_latest)
  [ -n "${VERSION}" ] || err 'could not resolve latest release tag'
fi

# Strip any leading "v" so the archive name template matches
# (goreleaser uses the bare semver in archive names).
VERSION_NUM=${VERSION#v}
ARCHIVE="ircat_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
URL="${BASE_URL}/${ARCHIVE}"

log "downloading ${ARCHIVE} from ${BASE_URL}"
TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT INT TERM

curl -fsSL "${URL}" -o "${TMP}/${ARCHIVE}" || err "download failed: ${URL}"
curl -fsSL "${BASE_URL}/checksums.txt" -o "${TMP}/checksums.txt" \
  || err 'checksums.txt download failed'

# Verify the SHA-256 checksum. The checksums file is produced
# by goreleaser; the line for our archive is what we compare
# against the locally-computed sum.
EXPECTED=$(grep "  ${ARCHIVE}\$" "${TMP}/checksums.txt" | awk '{print $1}')
[ -n "${EXPECTED}" ] || err "no checksum entry for ${ARCHIVE}"
ACTUAL=$(sha256sum "${TMP}/${ARCHIVE}" | awk '{print $1}')
[ "${EXPECTED}" = "${ACTUAL}" ] || err "checksum mismatch (got ${ACTUAL}, want ${EXPECTED})"
log "checksum ok"

# Optional cosign signature verification of checksums.txt.
if [ "${IRCAT_VERIFY:-0}" = "1" ]; then
  command -v cosign >/dev/null || err 'IRCAT_VERIFY=1 but cosign not in PATH'
  curl -fsSL "${BASE_URL}/checksums.txt.sig" -o "${TMP}/checksums.txt.sig" \
    || err 'checksums.txt.sig download failed'
  log 'verifying checksums.txt signature with cosign'
  cosign verify-blob \
    --certificate-identity-regexp "https://github.com/${REPO}/.*" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --signature "${TMP}/checksums.txt.sig" \
    "${TMP}/checksums.txt" >/dev/null
  log 'cosign verify ok'
fi

tar -xzf "${TMP}/${ARCHIVE}" -C "${TMP}"

INSTALL_PATH="${PREFIX}/ircat"
log "installing to ${INSTALL_PATH}"
if [ -w "${PREFIX}" ]; then
  install -m 0755 "${TMP}/ircat" "${INSTALL_PATH}"
else
  sudo install -m 0755 "${TMP}/ircat" "${INSTALL_PATH}"
fi

# Print the installed version so the user knows the install
# worked end-to-end.
"${INSTALL_PATH}" --version || true

cat <<EOF

ircat ${VERSION} installed to ${INSTALL_PATH}

Next steps:
  1. Copy a config: curl -fsSL https://raw.githubusercontent.com/${REPO}/main/docker/default-config.yaml -o /etc/ircat/config.yaml
  2. Edit /etc/ircat/config.yaml for your network
  3. Start the server: ircat server --config /etc/ircat/config.yaml

For container deployments, see docker-compose.yml in the repo or pull
the matching tag:
  docker pull ghcr.io/${REPO}:${VERSION}
EOF
