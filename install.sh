#!/usr/bin/env bash
# tf-triage installer
# Usage: curl -sSfL https://raw.githubusercontent.com/balmha/tf-triage/main/install.sh | bash
#
# Options (via environment variables):
#   VERSION   - specific version to install (default: latest)
#   INSTALL_DIR - installation directory (default: /usr/local/bin)

set -euo pipefail

REPO="balmha/tf-triage"
BINARY="tf-triage"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS and architecture
detect_platform() {
    local os arch

    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$os" in
        linux)  os="linux" ;;
        darwin) os="darwin" ;;
        *)
            echo "Error: Unsupported operating system: $os" >&2
            echo "  tf-triage supports linux and darwin (macOS)" >&2
            exit 1
            ;;
    esac

    case "$arch" in
        x86_64|amd64)  arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *)
            echo "Error: Unsupported architecture: $arch" >&2
            echo "  tf-triage supports amd64 and arm64" >&2
            exit 1
            ;;
    esac

    echo "${os}_${arch}"
}

# Get latest release tag from GitHub API
get_latest_version() {
    local latest
    latest="$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' \
        | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"

    if [ -z "$latest" ]; then
        echo "Error: Could not determine latest version" >&2
        echo "  Check https://github.com/${REPO}/releases for available versions" >&2
        exit 1
    fi

    echo "$latest"
}

main() {
    echo "Installing ${BINARY}..."

    local platform version download_url tmp_dir

    platform="$(detect_platform)"
    version="${VERSION:-$(get_latest_version)}"

    # Strip leading 'v' for the archive filename
    local version_num="${version#v}"

    download_url="https://github.com/${REPO}/releases/download/${version}/${BINARY}_${platform}.tar.gz"

    echo "  Platform: ${platform}"
    echo "  Version:  ${version}"
    echo "  URL:      ${download_url}"

    # Download and extract
    tmp_dir="$(mktemp -d)"
    trap 'rm -rf "$tmp_dir"' EXIT

    echo "  Downloading..."
    if ! curl -sSfL "$download_url" -o "${tmp_dir}/archive.tar.gz"; then
        echo "Error: Download failed" >&2
        echo "  Verify the version exists: https://github.com/${REPO}/releases/tag/${version}" >&2
        exit 1
    fi

    echo "  Extracting..."
    tar -xzf "${tmp_dir}/archive.tar.gz" -C "$tmp_dir"

    # Install binary
    echo "  Installing to ${INSTALL_DIR}/${BINARY}..."
    if [ -w "$INSTALL_DIR" ]; then
        mv "${tmp_dir}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    else
        echo "  (requires sudo)"
        sudo mv "${tmp_dir}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    fi

    chmod +x "${INSTALL_DIR}/${BINARY}"

    echo ""
    echo "Successfully installed ${BINARY} ${version} to ${INSTALL_DIR}/${BINARY}"
    echo ""
    echo "Verify with:"
    echo "  ${BINARY} --version"
}

main "$@"
