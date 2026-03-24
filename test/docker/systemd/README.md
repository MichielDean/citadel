# systemd-capable Docker base image

A Debian Bookworm container that boots with a real `systemd` init process.
Used as the base environment for installer tests that exercise
systemd-managed services (e.g., verifying that `cistern-castellarius.service`
installs and starts correctly via `install.sh`).

## Building

```sh
docker build -t cistern-systemd-test .
```

## Running

```sh
docker run --privileged --rm -d --name systemd-test cistern-systemd-test
```

Wait for systemd to reach the default target, then check its state:

```sh
docker exec systemd-test systemctl is-system-running
# expected: running   (exit 0)
# acceptable: degraded (exit 1) — some non-essential units failed to start
# not acceptable: failed / initializing
```

Stop the container:

```sh
docker stop systemd-test          # sends SIGRTMIN+3 → clean systemd shutdown
```

## Why `--privileged` is required

systemd acts as PID 1 and manages the Linux cgroup hierarchy.  Inside a normal
Docker container the kernel presents `/sys/fs/cgroup` as read-only or
restricted; systemd detects this and either refuses to start or enters a
permanently degraded state that cannot supervise child services.

`--privileged` grants the container what systemd needs:

| Capability / resource       | Why systemd needs it |
|-----------------------------|----------------------|
| Writable cgroup namespace   | Creates per-service cgroup slices for resource accounting and isolation |
| `CAP_SYS_ADMIN`             | Mounts the cgroup hierarchy and manages kernel namespaces |
| `CAP_SYS_PTRACE`            | Used by `systemd-journald` to read process metadata |
| Full `/sys/fs/cgroup` access | Moves processes between slices at runtime |

### Does `--privileged` share the host cgroup tree?

No.  `--privileged` grants an *isolated* cgroup namespace to the container.
The host's cgroup tree is neither visible to nor modified by the container.
This is a common misconception: `--privileged` is about *capability level*,
not about *sharing the host namespace*.

### Can a narrower capability set be used instead?

Yes, for hardened production environments.  The approximate minimum is:

```sh
docker run \
  --cap-add SYS_ADMIN \
  --cap-add SYS_PTRACE \
  --security-opt seccomp=unconfined \
  ...
```

`--privileged` is the correct choice for this test harness because it is
self-documenting, portable across kernel versions, and avoids subtle failures
caused by a missing capability on a newer kernel.

## No host-state leakage

This image does not bind-mount `/sys/fs/cgroup` or any other host directory.
Each `docker run` starts from a clean, immutable image layer.  State does not
persist between runs unless a volume is explicitly attached.

## Units masked at build time

The following units are masked (symlinked to `/dev/null`) because they require
hardware, VT consoles, or kernel interfaces unavailable inside Docker.
Leaving them enabled would cause systemd to report `failed` units on every boot.

| Masked unit | Reason |
|---|---|
| `systemd-udevd.service` | udev daemon — requires direct hardware access |
| `systemd-udev-settle.service` | Waits for udev events — never arrives in container |
| `systemd-udev-trigger.service` | Triggers udev kernel events — not applicable |
| `udev.service` | udev compatibility alias |
| `getty@tty1.service` | Virtual terminal getty — no VT in container |
| `serial-getty@ttyS0.service` | Serial console getty — not applicable |
| `sys-kernel-debug.mount` | Kernel debug filesystem — requires `debugfs` |
| `sys-kernel-tracing.mount` | Kernel tracing filesystem — restricted in container |
| `systemd-remount-fs.service` | Remounts root read-write — root is already writable |
| `systemd-machine-id-commit.service` | Commits machine-id to disk — not needed in ephemeral container |
| `systemd-firstboot.service` | First-boot interactive setup — not applicable |
| `systemd-update-utmp.service` | Updates utmp login records — no utmp in container |
| `systemd-update-utmp-runlevel.service` | Updates utmp on runlevel change — same |
