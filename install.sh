#!/usr/bin/env bash
#
# Junction install.sh — bootstrap a Junction binary onto the host.
#
# Canonical use (interactive):
#   bash <(curl -fsSL https://raw.githubusercontent.com/Rynaro/Junction/main/install.sh)
#
# Use from the Eidolons nexus harness:
#   JUNCTION_VERSION=0.1.2 INSTALL_DIR=$HOME/.eidolons/cache/junction@0.1.2 \
#     bash <(curl -fsSL ...install.sh)
#
# What it does:
#   1. Detects OS (linux/darwin) and arch (amd64/arm64) via uname.
#   2. Resolves the version (JUNCTION_VERSION env → 'latest' GitHub release).
#   3. Downloads junction-<os>-<arch> from the v<version> release.
#   4. Downloads SHA256SUMS and verifies the binary digest.
#   5. Places the binary at $INSTALL_DIR/junction (defaults to ~/.local/bin) and
#      sets +x.
#
# Bash 3.2 compatible (no associative arrays, no readarray). POSIX-leaning.

set -eu

JUNCTION_REPO="Rynaro/Junction"
# Destination directory resolution:
#   1. JUNCTION_INSTALL_DIR  — set by the eidolons-nexus harness (cache-aware).
#   2. INSTALL_DIR           — set by direct curl-bash callers who want a custom dir.
#   3. $HOME/.local/bin      — default for interactive curl-bash use.
INSTALL_DIR="${JUNCTION_INSTALL_DIR:-${INSTALL_DIR:-$HOME/.local/bin}}"

# ── Logging helpers (stderr; stdout reserved for the final success line) ──
say()  { printf '▸ %s\n' "$*" >&2; }
ok()   { printf '✓ %s\n' "$*" >&2; }
warn() { printf '⚠ %s\n' "$*" >&2; }
die()  { printf '✗ %s\n' "$*" >&2; exit 1; }

# ── Tool prerequisites ────────────────────────────────────────────────────
have() { command -v "$1" >/dev/null 2>&1; }
have curl || die "curl not found. Install curl to bootstrap Junction."

# Prefer sha256sum (Linux), fall back to shasum (macOS).
if have sha256sum; then
  SHA256_CMD="sha256sum"
elif have shasum; then
  SHA256_CMD="shasum -a 256"
else
  die "Neither sha256sum nor shasum found. Install one to verify the download."
fi

# ── Detect OS and arch ────────────────────────────────────────────────────
case "$(uname -s)" in
  Linux)  OS="linux" ;;
  Darwin) OS="darwin" ;;
  *)      die "Unsupported OS: $(uname -s). Junction ships linux and darwin builds." ;;
esac

case "$(uname -m)" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)             die "Unsupported arch: $(uname -m). Junction ships amd64 and arm64 builds." ;;
esac

ASSET="junction-${OS}-${ARCH}"

# ── Resolve version ───────────────────────────────────────────────────────
if [ -n "${JUNCTION_VERSION:-}" ] && [ "${JUNCTION_VERSION}" != "latest" ]; then
  VERSION="${JUNCTION_VERSION}"
else
  say "Resolving latest release tag for ${JUNCTION_REPO} …"
  # GitHub redirects releases/latest to the tagged release page. Follow + parse.
  LATEST_URL="$(curl -fsSLI -o /dev/null -w '%{url_effective}\n' \
                "https://github.com/${JUNCTION_REPO}/releases/latest")"
  # LATEST_URL ends in /tag/v<version> — strip prefix.
  VERSION="${LATEST_URL##*/v}"
  [ -n "$VERSION" ] || die "Could not resolve latest version from ${LATEST_URL}"
fi

# Strip a leading 'v' if the caller supplied it.
VERSION="${VERSION#v}"

# ── Download binary + checksums ───────────────────────────────────────────
BASE_URL="https://github.com/${JUNCTION_REPO}/releases/download/v${VERSION}"
say "Installing Junction v${VERSION} (${OS}/${ARCH}) into ${INSTALL_DIR} …"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

curl -fsSL -o "${TMPDIR}/${ASSET}" "${BASE_URL}/${ASSET}" \
  || die "Could not download ${BASE_URL}/${ASSET}"

curl -fsSL -o "${TMPDIR}/SHA256SUMS" "${BASE_URL}/SHA256SUMS" \
  || die "Could not download ${BASE_URL}/SHA256SUMS"

# ── Verify checksum ───────────────────────────────────────────────────────
say "Verifying ${ASSET} against SHA256SUMS …"
# SHA256SUMS lines look like: <hash>  <filename>. grep for ours, then check.
EXPECTED="$(grep -F "  ${ASSET}" "${TMPDIR}/SHA256SUMS" | awk '{print $1}')"
[ -n "$EXPECTED" ] || die "No SHA256SUMS entry for ${ASSET}"

ACTUAL="$(cd "$TMPDIR" && $SHA256_CMD "${ASSET}" | awk '{print $1}')"

if [ "$EXPECTED" != "$ACTUAL" ]; then
  die "SHA256 mismatch for ${ASSET}: expected=${EXPECTED} actual=${ACTUAL}"
fi
ok "SHA256 OK (${ACTUAL})"

# ── Place binary ──────────────────────────────────────────────────────────
mkdir -p "$INSTALL_DIR"
DEST="${INSTALL_DIR}/junction"
mv "${TMPDIR}/${ASSET}" "$DEST"
chmod +x "$DEST"

# Final success line on stdout (caller may parse this).
printf 'junction v%s installed at %s\n' "$VERSION" "$DEST"
