# ─── systemd-capable base image for installer tests ──────────────────────────
#
# Boots with systemd as PID 1 for testing systemd-managed service installers.
# Requires --privileged to give systemd a writable cgroup namespace.
# See README.md for capability details, masked units, and hardening options.
#
#   docker build -t cistern-systemd-test .
#   docker run --privileged --rm -d --name systemd-test cistern-systemd-test
#   docker exec systemd-test systemctl is-system-running   # → running|degraded
#   docker stop systemd-test

FROM debian:bookworm

# ENV container=docker is read by systemd to enable container-specific unit
# overrides and to skip hardware-only targets (kmod-static-nodes, cryptsetup,
# etc.) that would fail or hang indefinitely inside a container.
ENV container=docker

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        systemd \
        systemd-sysv \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Mask units that are incompatible with a container environment.
# These units require direct hardware access, VT consoles, or kernel
# interfaces that are unavailable inside Docker; leaving them enabled causes
# systemd to report 'failed' units on every boot.
#
# Masking = symlink to /dev/null in /etc/systemd/system/.  This takes
# precedence over unit files anywhere else on the search path and survives
# systemd package upgrades that would otherwise restore the files.
RUN for unit in \
        systemd-udevd.service \
        systemd-udev-settle.service \
        systemd-udev-trigger.service \
        udev.service \
        getty@tty1.service \
        serial-getty@ttyS0.service \
        sys-kernel-debug.mount \
        sys-kernel-tracing.mount \
        systemd-remount-fs.service \
        systemd-machine-id-commit.service \
        systemd-firstboot.service \
        systemd-update-utmp.service \
        systemd-update-utmp-runlevel.service \
    ; do \
        ln -sf /dev/null /etc/systemd/system/${unit}; \
    done

# Docker sends SIGTERM to PID 1 by default.  systemd's clean-shutdown signal
# is SIGRTMIN+3.  Override the stop signal so `docker stop` triggers an
# orderly unit shutdown rather than an abrupt kill.
STOPSIGNAL SIGRTMIN+3

# systemd must run as PID 1.  /lib/systemd/systemd is the canonical path on
# Debian; /sbin/init is a symlink to the same binary and is equally valid.
CMD ["/lib/systemd/systemd"]
