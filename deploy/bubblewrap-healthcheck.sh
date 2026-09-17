#!/usr/bin/env bash
set -euo pipefail

# Non-mutating host preflight. It creates no worktree files and only starts a
# short-lived isolated process. The systemd unit must allow AF_NETLINK because
# bwrap uses NETLINK_ROUTE while creating the private network namespace.
if ! command -v bwrap >/dev/null 2>&1; then
  printf 'Bubblewrap fehlt: installiere das Paket bubblewrap und starte taskboard.service neu.\n' >&2
  exit 1
fi

bind_args=(--ro-bind /usr /usr --ro-bind /bin /bin)
[[ -d /lib ]] && bind_args+=(--ro-bind /lib /lib)
[[ -d /lib64 ]] && bind_args+=(--ro-bind /lib64 /lib64)
output="$(bwrap --die-with-parent --unshare-all --new-session \
  "${bind_args[@]}" \
  --proc /proc --dev /dev --tmpfs /tmp \
  --setenv PATH /usr/bin:/bin \
  /bin/sh -c 'test ! -s /proc/net/route && printf sandbox-ok' 2>&1)" || {
  if [[ "$output" == *NETLINK_ROUTE* || "$output" == *loopback* ]]; then
    printf 'Bubblewrap kann den NETLINK_ROUTE-Socket nicht anlegen: erlaube AF_NETLINK nur in taskboard.service (RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK), führe daemon-reload aus und starte den Dienst neu.\n' >&2
  else
    printf 'Bubblewrap-Sandbox fehlgeschlagen: %s\nPrüfe User-/Mount-Namespaces und die RestrictAddressFamilies-Einstellung von taskboard.service.\n' "$output" >&2
  fi
  exit 1
}

if [[ "$output" != "sandbox-ok" ]]; then
  printf 'Bubblewrap-Sandbox lieferte kein Erfolgssignal: %s\n' "$output" >&2
  exit 1
fi
printf 'Bubblewrap-Sandbox: ok\n'
