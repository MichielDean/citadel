#!/usr/bin/env bash
set -euo pipefail

# Bullet Farm installer
# Usage: curl -sSf https://raw.githubusercontent.com/MichielDean/bullet-farm/main/install.sh | bash

REPO='github.com/MichielDean/bullet-farm'
BT_DIR="${HOME}/.bullet-farm"
MIN_GO_VERSION="1.22"

# --- colors (disabled if not a tty) ---
if [ -t 1 ]; then
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  YELLOW='\033[1;33m'
  BOLD='\033[1m'
  NC='\033[0m'
else
  RED='' GREEN='' YELLOW='' BOLD='' NC=''
fi

info()  { printf "${GREEN}>>>${NC} %s\n" "$*"; }
warn()  { printf "${YELLOW}warning:${NC} %s\n" "$*"; }
error() { printf "${RED}error:${NC} %s\n" "$*" >&2; }
fatal() { error "$@"; exit 1; }

# --- check_go: require go >= MIN_GO_VERSION ---
check_go() {
  if ! command -v go &>/dev/null; then
    fatal "Go is not installed. Install Go ${MIN_GO_VERSION}+ from https://go.dev/dl/ and try again."
  fi

  local go_version
  go_version="$(go version | sed -E 's/^go version go([0-9]+\.[0-9]+).*/\1/')"

  if [ -z "${go_version}" ]; then
    warn "Could not parse Go version. Continuing anyway."
    return
  fi

  local go_major go_minor min_major min_minor
  go_major="${go_version%%.*}"
  go_minor="${go_version#*.}"
  min_major="${MIN_GO_VERSION%%.*}"
  min_minor="${MIN_GO_VERSION#*.}"

  if [ "${go_major}" -lt "${min_major}" ] || \
     { [ "${go_major}" -eq "${min_major}" ] && [ "${go_minor}" -lt "${min_minor}" ]; }; then
    fatal "Go ${go_version} found, but ${MIN_GO_VERSION}+ is required. Upgrade at https://go.dev/dl/"
  fi

  info "Go ${go_version} found"
}

# --- install_bf: install via go install ---
install_bf() {
  info "Installing bf..."
  CGO_ENABLED=1 go install "${REPO}/cmd/bf@latest"

  # Verify the binary is on PATH.
  if command -v bf &>/dev/null; then
    info "bf installed at $(command -v bf)"
  else
    local gobin
    gobin="$(go env GOBIN)"
    if [ -z "${gobin}" ]; then
      gobin="$(go env GOPATH)/bin"
    fi
    warn "bf installed to ${gobin}/bf but it's not on your PATH."
    warn "Add this to your shell profile:  export PATH=\"\${PATH}:${gobin}\""
  fi
}

# --- setup_dirs: create ~/.bullet-farm structure ---
setup_dirs() {
  info "Setting up ${BT_DIR}..."
  mkdir -p "${BT_DIR}/sandboxes" "${BT_DIR}/logs"
}

# --- add_shell_completion: write completion for bash/zsh ---
add_shell_completion() {
  local shell_name
  shell_name="$(basename "${SHELL:-bash}")"

  case "${shell_name}" in
    bash)
      local comp_dir="${HOME}/.local/share/bash-completion/completions"
      mkdir -p "${comp_dir}"
      if command -v bf &>/dev/null; then
        bf completion bash > "${comp_dir}/bf" 2>/dev/null || true
        info "Bash completion installed to ${comp_dir}/bf"
      fi
      ;;
    zsh)
      local comp_dir="${HOME}/.zsh/completions"
      mkdir -p "${comp_dir}"
      if command -v bf &>/dev/null; then
        bf completion zsh > "${comp_dir}/_bf" 2>/dev/null || true
        info "Zsh completion installed to ${comp_dir}/_bf"
        if ! grep -q 'fpath.*\.zsh/completions' "${HOME}/.zshrc" 2>/dev/null; then
          warn "Add to your .zshrc:  fpath=(~/.zsh/completions \$fpath)"
        fi
      fi
      ;;
    *)
      warn "Shell completion not configured for ${shell_name}. Run: bf completion --help"
      ;;
  esac
}

# --- print_success ---
print_success() {
  printf "\n"
  info "${BOLD}Bullet Farm installed.${NC}"
  printf "\n"
  printf "Quick start:\n"
  printf "  bf queue add --title \"My first task\" --repo github.com/you/yourrepo\n"
  printf "  bf farm start\n"
  printf "\n"
  printf "Configuration:\n"
  printf "  Queue DB:   %s/queue.db\n" "${BT_DIR}"
  printf "  Sandboxes:  %s/sandboxes/\n" "${BT_DIR}"
  printf "  Logs:       %s/logs/\n" "${BT_DIR}"
  printf "\n"
}

main() {
  printf "${BOLD}Bullet Farm Installer${NC}\n\n"
  check_go
  install_bf
  setup_dirs
  add_shell_completion
  print_success
}

main "$@"
