#!/bin/bash

# Removes preinstalled tooling that Fleet's e2e jobs never touch.
#
# The multi-cluster job runs three k3d clusters as six containers sharing the
# runner's root filesystem. On a private repository that filesystem is 72 GB
# and arrives 82% full, leaving ~13.5 GiB for a job that consumes ~11.5 GiB, so
# it finishes within half a gigabyte of the 5% threshold at which kubelet
# declares DiskPressure, taints the nodes NoSchedule and evicts pods.
#
# Measured contents of these paths on ubuntu24/20260816.277, 25.6 GB in total.
# Removal happens in parallel because the runner has two cores.
#
# THIS SCRIPT DELETES SYSTEM DIRECTORIES. It only does so on an ephemeral
# GitHub-hosted runner, where the machine is discarded at the end of the job.
# Anywhere else it refuses and exits without touching anything. Set
# FLEET_FORCE_DISK_PRUNE=yes to override that, and DRY_RUN=1 to list what
# would be removed.
#
# This script never fails the job.

set -u

PRUNE=(
  /usr/local/lib/android         # 11.07 GB
  /usr/share/dotnet              #  5.66 GB
  /usr/local/.ghcup              #  3.74 GB
  /usr/share/swift               #  3.37 GB
  /opt/hostedtoolcache/CodeQL    #  1.73 GB
)

# Paths that must never be handed to rm, whatever PRUNE grows into later.
PROTECTED=(
  / /bin /boot /dev /etc /home /lib /lib64 /opt /proc /root /run /sbin /srv
  /sys /tmp /usr /usr/bin /usr/lib /usr/local /usr/local/bin /usr/local/lib
  /usr/sbin /usr/share /var /opt/hostedtoolcache
)

avail() { df -Pk / | awk 'NR==2 {print $4}'; }
gib() { awk -v k="$1" 'BEGIN {printf "%.2f GiB", k/1048576}'; }

# An ephemeral GitHub-hosted runner, not someone's workstation and not a
# persistent self-hosted machine. Every one of these is set by the Actions
# runner itself; ImageOS exists only on GitHub's prebuilt images.
on_disposable_runner() {
  [ "${GITHUB_ACTIONS:-}" = "true" ] &&
    [ "${RUNNER_ENVIRONMENT:-}" = "github-hosted" ] &&
    [ -n "${ImageOS:-}" ] &&
    [ -d /home/runner/work ]
}

# Refuses anything that is not a deep, absolute, literal directory path.
safe_to_remove() {
  local path="$1" protected

  case "$path" in
    /*) ;;
    *) echo "refusing relative path: $path" >&2; return 1 ;;
  esac
  case "$path" in
    *..*|*"*"*|*"?"*) echo "refusing wildcard or traversal: $path" >&2; return 1 ;;
  esac
  # At least three components deep, so /usr or /opt/foo can never match.
  case "$path" in
    /*/*/*) ;;
    *) echo "refusing shallow path: $path" >&2; return 1 ;;
  esac
  for protected in "${PROTECTED[@]}"; do
    if [ "$path" = "$protected" ]; then
      echo "refusing protected path: $path" >&2
      return 1
    fi
  done
  [ -d "$path" ] || return 1
  [ -L "$path" ] && { echo "refusing symlink: $path" >&2; return 1; }
  return 0
}

echo "::group::Free disk space"

if ! on_disposable_runner && [ "${FLEET_FORCE_DISK_PRUNE:-}" != "yes" ]; then
  echo "Not running on an ephemeral GitHub-hosted runner, skipping."
  echo "This script deletes system directories and would have removed:"
  printf '  %s\n' "${PRUNE[@]}"
  echo "Set FLEET_FORCE_DISK_PRUNE=yes if that is genuinely what you want."
  echo "::endgroup::"
  exit 0
fi

before=$(avail)
echo "available before: $(gib "$before")"

for path in "${PRUNE[@]}"; do
  safe_to_remove "$path" || continue
  if [ "${DRY_RUN:-}" = "1" ]; then
    echo "would remove $path ($(sudo du -sxm "$path" 2>/dev/null | cut -f1) MB)"
    continue
  fi
  echo "removing $path"
  sudo rm -rf "$path" &
done
wait

after=$(avail)
echo "available after:  $(gib "$after")"
echo "reclaimed:        $(gib $((after - before)))"
df -h /
echo "::endgroup::"
