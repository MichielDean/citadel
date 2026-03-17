#!/usr/bin/env bash
set -euo pipefail

# Cistern installer
# Usage: curl -sSf https://raw.githubusercontent.com/MichielDean/Cistern/main/install.sh | bash

REPO='github.com/MichielDean/cistern'
CT_DIR="${HOME}/.cistern"
MIN_GO_VERSION="1.22"

# --- colors (disabled if not a tty) ---
if [ -t 1 ]; then
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  YELLOW='\033[1;33m'
  BLUE='\033[0;34m'
  BOLD='\033[1m'
  NC='\033[0m'
else
  RED='' GREEN='' YELLOW='' BLUE='' BOLD='' NC=''
fi

info()  { printf "${GREEN}>>>${NC} %s\n" "$*"; }
warn()  { printf "${YELLOW}warning:${NC} %s\n" "$*"; }
error() { printf "${RED}error:${NC} %s\n" "$*" >&2; }
fatal() { error "$@"; exit 1; }
step()  { printf "\n${BLUE}━━━${NC} ${BOLD}%s${NC}\n" "$*"; }

# resolve_gobin returns the directory where `go install` places binaries.
resolve_gobin() {
  local gobin
  gobin="$(go env GOBIN 2>/dev/null)"
  if [ -z "${gobin}" ]; then
    gobin="$(go env GOPATH 2>/dev/null)/bin"
  fi
  echo "${gobin}"
}

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

  info "Go ${go_version} ✓"
}

# --- check_deps: verify runtime dependencies ---
check_deps() {
  local missing=()

  # tmux is required to spawn Claude Code sessions.
  if ! command -v tmux &>/dev/null; then
    missing+=("tmux")
  else
    info "tmux $(tmux -V | awk '{print $2}') ✓"
  fi

  # claude CLI is required to run AI steps.
  if ! command -v claude &>/dev/null; then
    missing+=("claude (Claude Code CLI)")
  else
    info "claude CLI ✓"
  fi

  # git is required for sandbox management.
  if ! command -v git &>/dev/null; then
    missing+=("git")
  else
    info "git $(git --version | awk '{print $3}') ✓"
  fi

  if [ ${#missing[@]} -gt 0 ]; then
    printf "\n"
    error "Missing required dependencies:"
    for dep in "${missing[@]}"; do
      printf "  • %s\n" "${dep}" >&2
    done
    printf "\n"
    if [[ " ${missing[*]} " == *"tmux"* ]]; then
      printf "  Install tmux:  apt install tmux  /  brew install tmux\n" >&2
    fi
    if [[ " ${missing[*]} " == *"claude"* ]]; then
      printf "  Install Claude Code CLI:  npm install -g @anthropic-ai/claude-code\n" >&2
    fi
    printf "\n"
    fatal "Install missing dependencies and re-run."
  fi
}

# --- check_api_key: verify ANTHROPIC_API_KEY is set ---
check_api_key() {
  if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
    printf "\n"
    warn "ANTHROPIC_API_KEY is not set."
    warn "Cistern needs this to run AI agent steps."
    printf "\n"
    printf "  Add to your shell profile (~/.bashrc, ~/.zshrc, etc.):\n"
    printf "    export ANTHROPIC_API_KEY='sk-ant-...'\n"
    printf "\n"
    printf "  Then reload your shell and re-run this installer.\n"
    printf "\n"
    fatal "ANTHROPIC_API_KEY must be set before installing."
  fi
  info "ANTHROPIC_API_KEY ✓"
}

# --- configure_git: ensure go module fetching works for private repos ---
configure_git() {
  # Rewrite HTTPS GitHub URLs to SSH so `go install` works with SSH key auth.
  local existing
  existing="$(git config --global url."git@github.com:".insteadOf 2>/dev/null || true)"
  if [ "${existing}" != "https://github.com/" ]; then
    git config --global url."git@github.com:".insteadOf "https://github.com/"
    info "Git configured to use SSH for GitHub ✓"
  else
    info "Git SSH rewrite already configured ✓"
  fi

  # Tell the Go toolchain to skip the public checksum database for this repo.
  export GOPRIVATE="${GOPRIVATE:+${GOPRIVATE},}github.com/MichielDean/cistern"
  info "GOPRIVATE set ✓"
}

# --- install_ct: install via go install ---
install_ct() {
  info "Installing ct..."
  CGO_ENABLED=1 GOPRIVATE="github.com/MichielDean/cistern" go install "${REPO}/cmd/ct@latest"

  local gobin ct_bin
  gobin="$(resolve_gobin)"
  ct_bin="${gobin}/ct"

  if [ ! -x "${ct_bin}" ]; then
    fatal "ct binary not found at ${ct_bin} after install — check your Go setup"
  fi

  local ct_version
  ct_version="$(${ct_bin} version 2>/dev/null | sed 's/^ct //' || echo 'dev')"
  info "ct ${ct_version} installed at ${ct_bin}"
}

# --- ensure_path: add Go bin dir to shell profile if not already there ---
ensure_path() {
  local gobin
  gobin="$(resolve_gobin)"

  # Detect the user's shell profile file.
  local profile=""
  local shell_name
  shell_name="$(basename "${SHELL:-bash}")"
  case "${shell_name}" in
    zsh)  profile="${ZDOTDIR:-${HOME}}/.zshrc" ;;
    bash) profile="${HOME}/.bashrc" ;;
    *)    profile="${HOME}/.profile" ;;
  esac

  local export_line="export PATH=\"\${PATH}:${gobin}\""

  if grep -qF "${gobin}" "${profile}" 2>/dev/null; then
    info "${gobin} already in ${profile} ✓"
  else
    printf '\n# Added by Cistern installer\n%s\n' "${export_line}" >> "${profile}"
    info "Added ${gobin} to ${profile}"
    # Export for the remainder of this script session.
    export PATH="${PATH}:${gobin}"
  fi
}

# --- setup_dirs: create ~/.cistern structure ---
setup_dirs() {
  info "Setting up ${CT_DIR}..."
  mkdir -p "${CT_DIR}/sandboxes" "${CT_DIR}/logs"
}

# --- create_config: initialize cistern if not already done ---
create_config() {
  local cfg="${CT_DIR}/cistern.yaml"

  if [ -f "${cfg}" ]; then
    info "Config already exists at ${cfg} — skipping"
    return
  fi

  info "Initializing Cistern..."
  local gobin
  gobin="$(resolve_gobin)"
  "${gobin}/ct" init > /dev/null
  info "Config and aqueduct files created"
}

# --- add_shell_completion: write completion for bash/zsh ---
add_shell_completion() {
  local gobin ct_bin shell_name
  gobin="$(resolve_gobin)"
  ct_bin="${gobin}/ct"
  shell_name="$(basename "${SHELL:-bash}")"

  case "${shell_name}" in
    bash)
      local comp_dir="${HOME}/.local/share/bash-completion/completions"
      mkdir -p "${comp_dir}"
      if "${ct_bin}" completion bash > "${comp_dir}/ct" 2>/dev/null; then
        info "Bash completion installed"
      fi
      ;;
    zsh)
      local comp_dir="${HOME}/.zsh/completions"
      mkdir -p "${comp_dir}"
      if "${ct_bin}" completion zsh > "${comp_dir}/_ct" 2>/dev/null; then
        info "Zsh completion installed"
      fi
      if ! grep -q 'fpath.*\.zsh/completions' "${HOME}/.zshrc" 2>/dev/null; then
        warn "Add to your .zshrc:  fpath=(~/.zsh/completions \$fpath)"
      fi
      ;;
    *)
      warn "Shell completion not configured for ${shell_name}. Run: ct completion --help"
      ;;
  esac
}

# --- print_success ---
print_success() {
  printf "\n"
  printf "${GREEN}${BOLD}✓ Cistern installed${NC}\n"
  printf "\n"
  printf "${BOLD}Next steps:${NC}\n"
  printf "\n"
  printf "  1. Edit your config:\n"
  printf "     ${BLUE}%s/cistern.yaml${NC}\n" "${CT_DIR}"
  printf "     Add at least one repo with a URL and operator names.\n"
  printf "\n"
  printf "  2. Add a droplet:\n"
  printf "     ${BLUE}ct droplet add --title \"My first task\" --repo <repo-name>${NC}\n"
  printf "\n"
  printf "  3. Wake the Castellarius:\n"
  printf "     ${BLUE}ct castellarius start${NC}\n"
  printf "\n"
  printf "${BOLD}Paths:${NC}\n"
  printf "  Config:     %s/cistern.yaml\n" "${CT_DIR}"
  printf "  Aqueducts:  %s/aqueduct/\n" "${CT_DIR}"
  printf "  Cataractae: %s/cataractae/\n" "${CT_DIR}"
  printf "  Queue DB:   %s/cistern.db\n" "${CT_DIR}"
  printf "  Sandboxes:  %s/sandboxes/\n" "${CT_DIR}"
  printf "\n"
  printf "${BOLD}Docs:${NC} https://github.com/MichielDean/Cistern\n"
  printf "\n"
}

main() {
  printf "${BOLD}Cistern Installer${NC}\n\n"
  step "Checking Go"
  check_go
  step "Checking dependencies"
  check_deps
  step "Checking API key"
  check_api_key
  step "Configuring git"
  configure_git
  step "Installing ct"
  install_ct
  step "Updating PATH"
  ensure_path
  step "Setting up directories"
  setup_dirs
  step "Initializing config"
  create_config
  step "Shell completion"
  add_shell_completion
  print_success
}

main "$@"
