# Docker systemd installer test infrastructure

Smoke tests for the Cistern installer, exercised inside a systemd-capable
Ubuntu 24.04 container.  The container starts real systemd as PID 1 and then
verifies that the installer artifacts (`ct`, `start-castellarius.sh`, the
fakeagent Claude stub) work correctly without `pass` or GPG.

## Prerequisites

- Docker with support for `--privileged` containers
- Repository cloned locally

## Build the test image

Run from the **repository root**:

```bash
bash tests/installer/build.sh
```

The build uses a multi-stage Dockerfile:

1. **builder** (`golang:1.26`) — compiles `ct` and `fakeagent`
2. **runtime** (`jrei/systemd-ubuntu:24.04`) — copies binaries in, no pass/GPG

## Run the smoke tests

```bash
# 1. Build the image
bash tests/installer/build.sh

# 2. Start the container with systemd as PID 1
docker run \
  --privileged \
  --rm \
  -d \
  --name cistern-installer-test \
  cistern/installer-test:latest

# 3. Run the test suite (run-tests.sh waits for systemd internally)
docker exec cistern-installer-test /usr/local/bin/run-tests.sh

# 4. Stop and remove the container
docker stop cistern-installer-test
```

### Required Docker flags

| Flag | Reason |
|------|--------|
| `--privileged` | Grants systemd the capabilities it needs to manage cgroups, mount namespaces, and device nodes. Without it, systemd cannot start. |
| `-d` | Run detached — systemd is PID 1 and boots in the background. |
| `--rm` | Clean up the container automatically when it stops. |

No additional volume mounts or tmpfs flags are required: `jrei/systemd-ubuntu`
pre-configures the cgroup mounts and tmpfs overlays that systemd needs.

## Test output format

Each test emits one line to stdout:

```
[PASS] test_name
[FAIL] test_name: error detail
```

The script exits `0` if all tests pass, `1` if any fail — making it suitable
as a GitHub Actions step:

```yaml
- name: Run installer smoke tests
  run: |
    docker run --privileged --rm -d --name cistern-test cistern/installer-test:latest
    docker exec cistern-test /usr/local/bin/run-tests.sh
    docker stop cistern-test
```

## Tests

### Smoke tests

| Name | What it checks |
|------|---------------|
| `systemd_multi_user_target` | `systemd` reached `multi-user.target` inside the container |
| `ct_binary_version` | `ct version` exits 0 |
| `fakeagent_print_output` | `claude --print` returns the hardcoded JSON proposal array |
| `claude_on_path` | `claude` resolves via `exec.LookPath` (on `PATH`) |
| `no_pass_installed` | `pass` password manager is absent |
| `ct_init_creates_config` | `ct init` creates `~/.cistern/cistern.yaml` |
| `ct_doctor_claude_found` | `ct doctor` reports the `claude` CLI as found |
| `start_castellarius_script_executable` | `/usr/local/bin/start-castellarius.sh` is present and executable |

### Integration scenarios

Each scenario resets container state via `_reset_scenario_state` before running to prevent cross-contamination. Scenarios exercise end-to-end service lifecycle with `cistern-castellarius.service` under systemd.

**Scenario 1 — Fresh install**

Preconditions: no `~/.cistern` present, valid `ANTHROPIC_API_KEY` in `~/.cistern/env`.

| Assertion | What it checks |
|-----------|---------------|
| `fresh_install_ct_init` | `ct init` exits 0 and bootstraps `~/.cistern` |
| `fresh_install_service_active` | `cistern-castellarius.service` reaches `active (running)` state |
| `fresh_install_ct_doctor_passes` | `ct doctor` exits 0 with no errors |

**Scenario 2 — Missing credentials**

Preconditions: `ct init` has run; `~/.cistern/env` is absent (no API key).

| Assertion | What it checks |
|-----------|---------------|
| `missing_creds_ct_init` | `ct init` exits 0 |
| `missing_creds_service_logged_error` | service enters `failed` state and the journal contains a message referencing missing credentials (not a silent crash) |
| `missing_creds_ct_doctor_message` | `ct doctor` output contains a message referencing missing credentials |
| `missing_creds_ct_doctor_exits_nonzero` | `ct doctor` exits non-zero |

**Scenario 3 — Wrong / expired token**

Preconditions: `~/.cistern/env` contains a syntactically valid but rejected API key; `~/.claude/.credentials.json` contains an expired OAuth token (`expiresAt` in the past).

| Assertion | What it checks |
|-----------|---------------|
| `wrong_token_ct_init` | `ct init` exits 0 |
| `wrong_token_service_startup_error` | service enters `failed` state and the journal contains an actionable message mentioning `invalid` or `expired` token |
| `wrong_token_ct_doctor_surfaces_error` | `ct doctor` output surfaces the same expired-token error |
| `wrong_token_ct_doctor_exits_nonzero` | `ct doctor` exits non-zero |

**Scenario 4 — Upgrade**

Preconditions: `~/.cistern` is pre-seeded with a fixture representing a prior-version install (stale config keys, old binary path) and existing credentials.

| Assertion | What it checks |
|-----------|---------------|
| `upgrade_ct_init` | `ct init` exits 0 when run over an existing install |
| `upgrade_credentials_preserved` | existing `ANTHROPIC_API_KEY` in `~/.cistern/env` is not overwritten |
| `upgrade_service_active` | `cistern-castellarius.service` reaches `active (running)` state after upgrade |
| `upgrade_ct_doctor_passes` | `ct doctor` exits 0 with no errors after upgrade |

## Image contents

| Path | Source | Description |
|------|--------|-------------|
| `/usr/local/bin/ct` | built from `./cmd/ct` | Cistern CLI |
| `/usr/local/bin/claude` | built from `./internal/testutil/fakeagent/` | Claude CLI stub (no real LLM) |
| `/usr/local/bin/start-castellarius.sh` | `start-castellarius.sh` in repo root | Wrapper for `ct castellarius start` |
| `/usr/local/bin/run-tests.sh` | `tests/installer/run-tests.sh` | Smoke test runner |

## Credential story (no pass / GPG required)

The test image does **not** install `pass` or `gnupg`.

- The `fakeagent` Claude stub handles all agent invocations without making
  real API calls, so no `ANTHROPIC_API_KEY` is needed for the smoke tests.
- `ct init` and `ct doctor` do not require `pass` or GPG at any point.
- For integration runs where a real API key is needed, pass it as an
  environment variable: `docker run -e ANTHROPIC_API_KEY=sk-ant-... ...`

## Overriding the image tag

```bash
CISTERN_TEST_IMAGE=myrepo/cistern-test:pr-42 bash tests/installer/build.sh
```
