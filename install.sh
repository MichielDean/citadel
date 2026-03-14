#!/usr/bin/env bash
set -euo pipefail

# Bullet Farm installer
# Usage: curl -sSf https://raw.githubusercontent.com/MichielDean/bullet-farm/main/install.sh | bash

REPO='github.com/MichielDean/bullet-farm'
BF_DIR="${HOME}/.bullet-farm"
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
    warn "Bullet Farm needs this to run AI agent steps."
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

# --- install_bf: install via go install ---
install_bf() {
  info "Installing bf..."
  CGO_ENABLED=1 go install "${REPO}/cmd/bf@latest"

  # Verify the binary is on PATH.
  if command -v bf &>/dev/null; then
    info "bf $(bf version 2>/dev/null || echo '?') installed at $(command -v bf)"
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
  info "Setting up ${BF_DIR}..."
  mkdir -p "${BF_DIR}/sandboxes" "${BF_DIR}/logs"
}

# --- create_config: write starter config.yaml if none exists ---
create_config() {
  local cfg="${BF_DIR}/config.yaml"

  if [ -f "${cfg}" ]; then
    info "Config already exists at ${cfg} — skipping"
    return
  fi

  info "Creating starter config at ${cfg}..."
  cat > "${cfg}" << 'YAML'
# Bullet Farm configuration
# Edit this file to add your repos and configure your farm.
#
# See https://github.com/MichielDean/bullet-farm for full documentation.

# Maximum total workers across all repos (0 = unlimited).
max_total_workers: 4

# How long to keep closed/escalated work items before purging.
retention_days: 90

# How often the background retention cleanup runs.
cleanup_interval: 24h

repos:
  # Example repo — replace with your own.
  # - name: my-project
  #   url: git@github.com:you/my-project.git
  #   prefix: mp
  #   workflow_path: ~/.bullet-farm/workflows/feature.yaml
  #   max_workers: 2
  #   workers:
  #     - name: worker-a
  #     - name: worker-b
YAML

  # Also create a workflows directory with the bundled feature workflow.
  mkdir -p "${BF_DIR}/workflows"

  local wf="${BF_DIR}/workflows/feature.yaml"
  if [ ! -f "${wf}" ]; then
    info "Creating default workflow at ${wf}..."
    cat > "${wf}" << 'YAML'
name: feature
steps:
  - name: implement
    type: agent
    role: implementer
    model: sonnet
    context: full_codebase
    max_iterations: 3
    timeout_minutes: 30
    on_pass: adversarial-review
    on_fail: blocked
    on_escalate: human

  - name: adversarial-review
    type: agent
    role: reviewer
    model: sonnet
    context: diff_only
    timeout_minutes: 15
    on_pass: qa
    on_fail: implement
    on_revision: implement
    on_escalate: human

  - name: qa
    type: agent
    role: qa
    model: haiku
    context: full_codebase
    timeout_minutes: 20
    on_pass: merge
    on_fail: implement
    on_escalate: human

  - name: merge
    type: automated
    timeout_minutes: 5
    on_pass: done
    on_fail: human
YAML
  fi
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
        info "Bash completion installed"
      fi
      ;;
    zsh)
      local comp_dir="${HOME}/.zsh/completions"
      mkdir -p "${comp_dir}"
      if command -v bf &>/dev/null; then
        bf completion zsh > "${comp_dir}/_bf" 2>/dev/null || true
        info "Zsh completion installed"
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
  printf "${GREEN}${BOLD}✓ Bullet Farm installed${NC}\n"
  printf "\n"
  printf "${BOLD}Next steps:${NC}\n"
  printf "\n"
  printf "  1. Edit your config:\n"
  printf "     ${BLUE}%s/config.yaml${NC}\n" "${BF_DIR}"
  printf "     Add at least one repo with a URL, prefix, and workers.\n"
  printf "\n"
  printf "  2. Add a work item:\n"
  printf "     ${BLUE}bf queue add --title \"My first task\" --repo <repo-name>${NC}\n"
  printf "\n"
  printf "  3. Start the farm:\n"
  printf "     ${BLUE}bf farm start --config ~/.bullet-farm/config.yaml${NC}\n"
  printf "\n"
  printf "${BOLD}Paths:${NC}\n"
  printf "  Config:     %s/config.yaml\n" "${BF_DIR}"
  printf "  Workflows:  %s/workflows/\n" "${BF_DIR}"
  printf "  Queue DB:   %s/queue.db\n" "${BF_DIR}"
  printf "  Sandboxes:  %s/sandboxes/\n" "${BF_DIR}"
  printf "\n"
  printf "${BOLD}Docs:${NC} https://github.com/MichielDean/bullet-farm\n"
  printf "\n"
}

main() {
  printf "${BOLD}Bullet Farm Installer${NC}\n\n"
  step "Checking Go"
  check_go
  step "Checking dependencies"
  check_deps
  step "Checking API key"
  check_api_key
  step "Installing bf"
  install_bf
  step "Setting up directories"
  setup_dirs
  step "Creating starter config"
  create_config
  step "Shell completion"
  add_shell_completion
  print_success
}

main "$@"
