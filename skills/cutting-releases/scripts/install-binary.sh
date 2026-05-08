#!/usr/bin/env bash
# Install a fullsend release binary locally after verifying its checksum.
#
# Usage: install-binary.sh <tag> [install-dir]
#
# Arguments:
#   tag          Release tag (e.g. v0.5.0)
#   install-dir  Where to place the binary (default: ~/.local/bin)
#
# The binary is installed as fullsend-<tag> so multiple versions can coexist.
set -euo pipefail

TAG="${1:?Usage: install-binary.sh <tag> [install-dir]}"
INSTALL_DIR="${2:-$HOME/.local/bin}"

ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCHIVE="fullsend_*_${OS}_${ARCH}.tar.gz"

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT
cd "$WORKDIR"

echo "Downloading ${TAG} for ${OS}/${ARCH}..."
gh release download "$TAG" \
  --repo fullsend-ai/fullsend \
  --pattern "${ARCHIVE}" \
  --pattern "checksums.txt" \
  --clobber

echo "Verifying checksum..."
grep "${OS}_${ARCH}.tar.gz" checksums.txt | sha256sum -c

echo "Extracting..."
tar xzf ${ARCHIVE} fullsend

mkdir -p "${INSTALL_DIR}"
mv fullsend "${INSTALL_DIR}/fullsend-${TAG}"
chmod +x "${INSTALL_DIR}/fullsend-${TAG}"

echo "Installed: ${INSTALL_DIR}/fullsend-${TAG}"
"${INSTALL_DIR}/fullsend-${TAG}" --version
