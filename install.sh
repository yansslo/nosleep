#!/usr/bin/env bash
set -euo pipefail

repo="${NOSLEEP_REPO:-yansslo/nosleep}"
install_dir="${NOSLEEP_INSTALL_DIR:-"$HOME/.local/bin"}"
binary_name="${NOSLEEP_BINARY_NAME:-nosleep}"

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  *)
    echo "nosleep currently only supports macOS." >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  arm64) arch="arm64" ;;
  x86_64) arch="x64" ;;
  *)
    echo "Unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

asset_name="nosleep-${os}-${arch}"

if [[ -n "${NOSLEEP_BINARY_URL:-}" ]]; then
  binary_url="$NOSLEEP_BINARY_URL"
elif [[ -n "${NOSLEEP_VERSION:-}" ]]; then
  binary_url="https://github.com/${repo}/releases/download/${NOSLEEP_VERSION}/${asset_name}"
else
  binary_url="https://github.com/${repo}/releases/latest/download/${asset_name}"
fi

tmp_file="$(mktemp)"

cleanup() {
  rm -f "$tmp_file"
}

trap cleanup EXIT

mkdir -p "$install_dir"

echo "Downloading ${asset_name} from ${binary_url}"
curl -fsSL "$binary_url" -o "$tmp_file"
chmod +x "$tmp_file"
mv "$tmp_file" "${install_dir}/${binary_name}"

echo "Installed ${binary_name} to ${install_dir}/${binary_name}"

case ":$PATH:" in
  *":${install_dir}:"*) ;;
  *)
    echo
    echo "Add ${install_dir} to your PATH to run ${binary_name} from any terminal:"
    echo "  export PATH=\"${install_dir}:\$PATH\""
    ;;
esac
