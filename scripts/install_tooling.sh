#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-all}" # core | lsp | all

log() { printf '[install] %s\n' "$*"; }
warn() { printf '[install][warn] %s\n' "$*" >&2; }
die() { printf '[install][error] %s\n' "$*" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }

if [[ "$MODE" != "core" && "$MODE" != "lsp" && "$MODE" != "all" ]]; then
  die "invalid mode '$MODE' (expected: core | lsp | all)"
fi

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
SUDO=""
if [[ "$(id -u)" -ne 0 ]] && has sudo; then
  SUDO="sudo"
fi

apt_updated=0
apt_update_once() {
  if [[ "$apt_updated" -eq 0 ]]; then
    log "apt-get update"
    $SUDO apt-get update -y
    apt_updated=1
  fi
}

install_brew_pkg() {
  local pkg="$1"
  if brew list --formula "$pkg" >/dev/null 2>&1; then
    log "brew package already installed: $pkg"
    return 0
  fi
  log "brew install $pkg"
  brew install "$pkg"
}

install_apt_pkg() {
  local pkg="$1"
  if dpkg -s "$pkg" >/dev/null 2>&1; then
    log "apt package already installed: $pkg"
    return 0
  fi
  apt_update_once
  log "apt-get install -y $pkg"
  $SUDO apt-get install -y "$pkg"
}

install_core() {
  case "$OS" in
    darwin*)
      has brew || die "Homebrew is required on macOS. Install from https://brew.sh/"
      install_brew_pkg go
      install_brew_pkg jq
      install_brew_pkg ripgrep
      install_brew_pkg node
      install_brew_pkg openjdk@21
      ;;
    linux*)
      has apt-get || die "This installer currently supports apt-based Linux for auto-install."
      install_apt_pkg golang-go
      install_apt_pkg jq
      install_apt_pkg ripgrep
      install_apt_pkg nodejs
      install_apt_pkg npm
      if ! dpkg -s openjdk-21-jdk >/dev/null 2>&1; then
        install_apt_pkg default-jdk
      else
        install_apt_pkg openjdk-21-jdk
      fi
      ;;
    *)
      die "unsupported OS: $OS"
      ;;
  esac
}

install_lsp() {
  has go || die "go is required to install gopls"
  has npm || die "npm is required to install tsserver/pyright"

  log "installing gopls"
  GO111MODULE=on go install golang.org/x/tools/gopls@latest

  log "installing TypeScript/JavaScript language server (typescript-language-server) and pyright"
  npm install -g typescript typescript-language-server pyright

  case "$OS" in
    darwin*)
      if has brew; then
        if brew info jdtls >/dev/null 2>&1; then
          install_brew_pkg jdtls
        else
          warn "brew formula 'jdtls' not found. Java LSP will remain optional."
        fi
      fi
      ;;
    linux*)
      if has apt-get; then
        if apt-cache show jdtls >/dev/null 2>&1; then
          install_apt_pkg jdtls
        else
          warn "apt package 'jdtls' not found. Install jdtls manually if Java LSP is required."
        fi
      fi
      ;;
  esac
}

case "$MODE" in
  core)
    install_core
    ;;
  lsp)
    install_lsp
    ;;
  all)
    install_core
    install_lsp
    ;;
esac

log "done"
