#!/usr/bin/env bash
set -euo pipefail

STRICT_LSP=false
if [[ "${1:-}" == "--strict-lsp" ]]; then
  STRICT_LSP=true
fi

ok() { printf '[doctor][ok] %s\n' "$*"; }
warn() { printf '[doctor][warn] %s\n' "$*" >&2; }
err() { printf '[doctor][error] %s\n' "$*" >&2; }
has() { command -v "$1" >/dev/null 2>&1; }

fail=0
missing_lsp=0

check_required() {
  local cmd="$1"
  local hint="$2"
  if has "$cmd"; then
    ok "$cmd: $(command -v "$cmd")"
  else
    err "$cmd missing ($hint)"
    fail=1
  fi
}

check_optional_lsp() {
  local cmd="$1"
  local name="$2"
  if has "$cmd"; then
    ok "$name: $(command -v "$cmd")"
  else
    warn "$name missing (optional, adapter will be marked unavailable)"
    missing_lsp=1
  fi
}

check_optional_gopls() {
  if has gopls; then
    ok "gopls (Go LSP): $(command -v gopls)"
    return
  fi
  if has go; then
    local fallback
    fallback="$(go env GOPATH 2>/dev/null)/bin/gopls"
    if [[ -x "$fallback" ]]; then
      ok "gopls (Go LSP): $fallback (from GOPATH/bin)"
      return
    fi
  fi
  warn "gopls (Go LSP) missing (optional, adapter will be marked unavailable)"
  missing_lsp=1
}

check_required go "install Go first"
check_required jq "install jq"
check_required rg "install ripgrep"
check_required node "install Node.js"
check_required npm "install npm"
check_required java "install JDK (Java runtime)"

check_optional_gopls
check_optional_lsp typescript-language-server "typescript-language-server (TypeScript/JavaScript LSP)"
check_optional_lsp tsserver "tsserver (legacy TypeScript server; non-LSP)"
check_optional_lsp pyright-langserver "pyright-langserver (Python LSP)"
check_optional_lsp jdtls "jdtls (Java LSP)"

if [[ "$STRICT_LSP" == "true" && "$missing_lsp" -eq 1 ]]; then
  err "strict LSP mode enabled and one or more LSP tools are missing"
  fail=1
fi

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

ok "tooling check passed"
