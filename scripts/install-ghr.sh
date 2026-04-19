#!/usr/bin/env bash
set -euo pipefail

REPO="dutifuldev/ghreplica"
BINARY="ghr"

usage() {
  cat <<'EOF'
Install the ghr CLI from GitHub Releases.

Usage:
  install-ghr.sh [--version vX.Y.Z] [--bin-dir DIR]

Options:
  --version  Install a specific release tag. Defaults to the latest release.
  --bin-dir  Install into this directory instead of the default location.
  --help     Show this help.

Environment:
  GHR_VERSION      Same as --version.
  GHR_INSTALL_DIR  Same as --bin-dir.
EOF
}

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

resolve_os() {
  case "$(uname -s)" in
    Linux) printf 'Linux' ;;
    Darwin) printf 'macOS' ;;
    *)
      echo "unsupported OS: $(uname -s)" >&2
      exit 1
      ;;
  esac
}

resolve_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'x86_64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *)
      echo "unsupported architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

latest_version() {
  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
    | head -n1
}

choose_bin_dir() {
  if [ -n "${INSTALL_DIR}" ]; then
    printf '%s' "${INSTALL_DIR}"
    return
  fi

  if [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then
    printf '/usr/local/bin'
    return
  fi

  if [ -d "/usr/local/bin" ] && command -v sudo >/dev/null 2>&1; then
    printf '/usr/local/bin'
    return
  fi

  printf '%s' "${HOME}/.local/bin"
}

install_binary() {
  local src="$1"
  local dest_dir="$2"
  local dest="${dest_dir}/${BINARY}"

  mkdir -p "${dest_dir}"

  if [ -w "${dest_dir}" ]; then
    install -m 0755 "${src}" "${dest}"
    return
  fi

  if command -v sudo >/dev/null 2>&1; then
    sudo install -m 0755 "${src}" "${dest}"
    return
  fi

  echo "cannot write to ${dest_dir} and sudo is not available" >&2
  exit 1
}

VERSION="${GHR_VERSION:-}"
INSTALL_DIR="${GHR_INSTALL_DIR:-}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --bin-dir)
      INSTALL_DIR="${2:-}"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

need_cmd curl
need_cmd tar
need_cmd install
need_cmd sed
need_cmd mktemp

OS="$(resolve_os)"
ARCH="$(resolve_arch)"

if [ -z "${VERSION}" ]; then
  VERSION="$(latest_version)"
fi

if [ -z "${VERSION}" ]; then
  echo "failed to resolve latest release version" >&2
  exit 1
fi

VERSION_NO_V="${VERSION#v}"
ASSET="${BINARY}_${VERSION_NO_V}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
BIN_DIR="$(choose_bin_dir)"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

echo "installing ${BINARY} ${VERSION} for ${OS} ${ARCH}"
echo "downloading ${URL}"

curl -fsSL -o "${TMPDIR}/${ASSET}" "${URL}"
tar -xzf "${TMPDIR}/${ASSET}" -C "${TMPDIR}"
install_binary "${TMPDIR}/${BINARY}" "${BIN_DIR}"

echo "installed ${BINARY} to ${BIN_DIR}/${BINARY}"

case ":${PATH}:" in
  *:"${BIN_DIR}":*)
    ;;
  *)
    echo "note: ${BIN_DIR} is not on your PATH"
    ;;
esac
