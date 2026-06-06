#!/usr/bin/env bash
#
# lint installer — downloads a prebuilt binary for your platform.
#
#   curl -fsSL https://raw.githubusercontent.com/Ichigo-Labs/lint/master/install.sh | bash
#
# No Go or C compiler required: this fetches a release binary, verifies its
# SHA-256 checksum, and drops it on your PATH.
#
# Environment overrides:
#   LINT_VERSION      install a specific release tag (e.g. v0.2.0). Default: latest.
#   LINT_INSTALL_DIR  directory to install into. Default: /usr/local/bin if
#                     writable, otherwise $HOME/.local/bin.
#
# The logic lives in main(), invoked on the final line, so that piping this
# script straight into bash (curl ... | bash) never executes a partial program
# if the download is truncated.
#
set -euo pipefail

main() {
  REPO="Ichigo-Labs/lint"
  BIN="lint"

  log() { printf '%s\n' "$*" >&2; }
  err() { printf 'install.sh: error: %s\n' "$*" >&2; exit 1; }

  command -v tar >/dev/null 2>&1 || err "tar is required but was not found"

  # --- pick a downloader ---------------------------------------------------
  if command -v curl >/dev/null 2>&1; then
    dlfile() { curl -fSL --retry 3 "$1" -o "$2"; }
  elif command -v wget >/dev/null 2>&1; then
    dlfile() { wget -q "$1" -O "$2"; }
  else
    err "need either curl or wget to download lint"
  fi

  # --- detect platform -----------------------------------------------------
  os="$(uname -s)"
  case "$os" in
    Linux)  os=linux ;;
    Darwin) os=darwin ;;
    *) err "unsupported OS '$os' — see https://github.com/${REPO}" ;;
  esac

  arch="$(uname -m)"
  case "$arch" in
    x86_64 | amd64)  arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) err "unsupported architecture '$arch' — see https://github.com/${REPO}" ;;
  esac

  asset="${BIN}_${os}_${arch}.tar.gz"

  # --- resolve download URLs -----------------------------------------------
  version="${LINT_VERSION:-}"
  if [ -n "$version" ]; then
    base="https://github.com/${REPO}/releases/download/${version}"
  else
    base="https://github.com/${REPO}/releases/latest/download"
  fi
  asset_url="${base}/${asset}"
  sums_url="${base}/checksums.txt"

  # --- choose install dir --------------------------------------------------
  install_dir="${LINT_INSTALL_DIR:-}"
  if [ -z "$install_dir" ]; then
    if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
      install_dir=/usr/local/bin
    else
      [ -n "${HOME:-}" ] || err "HOME is not set; set LINT_INSTALL_DIR to choose an install directory"
      install_dir="${HOME}/.local/bin"
    fi
  fi
  mkdir -p "$install_dir" || err "cannot create install directory: $install_dir"
  [ -w "$install_dir" ] || err "install directory is not writable: $install_dir (set LINT_INSTALL_DIR)"

  # --- download + verify ---------------------------------------------------
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  log "lint: downloading ${asset} (${version:-latest})..."
  dlfile "$asset_url" "$tmp/$asset" || err "download failed: $asset_url"

  log "lint: verifying checksum..."
  dlfile "$sums_url" "$tmp/checksums.txt" || err "could not download checksums.txt from $sums_url"

  want="$(awk -v f="$asset" '$2 == f {print $1}' "$tmp/checksums.txt" | head -n1)"
  [ -n "$want" ] || err "no checksum listed for $asset in checksums.txt"

  if command -v sha256sum >/dev/null 2>&1; then
    got="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    got="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
  else
    err "need sha256sum or shasum to verify the download"
  fi
  [ "$want" = "$got" ] || err "checksum mismatch for $asset
  expected: $want
  got:      $got"

  # --- extract + install ---------------------------------------------------
  tar -xzf "$tmp/$asset" -C "$tmp" "$BIN" || err "failed to extract $BIN from $asset"
  chmod +x "$tmp/$BIN"

  target="$install_dir/$BIN"
  mv -f "$tmp/$BIN" "$target" || err "failed to install to $target"

  log "lint: installed to $target"
  if "$target" --version >/dev/null 2>&1; then
    log "lint: $("$target" --version)"
  fi

  # --- PATH hint -----------------------------------------------------------
  case ":${PATH}:" in
    *":${install_dir}:"*) ;;
    *)
      log ""
      log "note: ${install_dir} is not on your PATH. Add it with:"
      log "  export PATH=\"${install_dir}:\$PATH\""
      ;;
  esac
}

main "$@"
