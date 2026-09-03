#!/bin/sh
# Operator-installed maintenance helper. Never invoke against an arbitrary host
# automatically: review deploy/systemd/backup.env.example and the recovery guide.
set -eu
umask 077
mode=rotate
case "$#:$*" in
  0:) ;;
  1:--recover) mode=recover ;;
  *) echo 'Usage: backup-systemd.sh [--recover]' >&2; exit 2 ;;
esac

: "${DIFFMIND_SERVICE:?set the exact systemd unit to maintain}"
: "${DIFFMIND_BINARY:?set the absolute Diffmind binary path}"
: "${DIFFMIND_HOME:?set the workspace path}"
: "${DIFFMIND_BACKUP_DIRECTORY:?set the private backup catalog path}"
: "${DIFFMIND_BACKUP_KEEP_LAST:?set retained snapshot count}"
: "${DIFFMIND_BACKUP_LOCK:?set a private absolute lock-file path}"
case "$DIFFMIND_SERVICE" in
  -*|*[!a-zA-Z0-9_.@-]*|'') echo 'Invalid systemd service name' >&2; exit 2 ;;
  *.service) ;;
  *) echo 'Specify a .service unit' >&2; exit 2 ;;
esac
test "$DIFFMIND_SERVICE" != diffmind-backup.service || { echo 'Backup service must not stop itself' >&2; exit 2; }
case "$DIFFMIND_BACKUP_KEEP_LAST" in
  ''|*[!0-9]*|?????*) echo 'keep-last must be 1..1000' >&2; exit 2 ;;
esac
if [ "$DIFFMIND_BACKUP_KEEP_LAST" -lt 1 ] || [ "$DIFFMIND_BACKUP_KEEP_LAST" -gt 1000 ]; then
  echo 'keep-last must be 1..1000' >&2; exit 2
fi
for value in "$DIFFMIND_BINARY" "$DIFFMIND_HOME" "$DIFFMIND_BACKUP_DIRECTORY" "$DIFFMIND_BACKUP_LOCK"; do
  case "$value" in /*) ;; *) echo 'All paths must be absolute' >&2; exit 2 ;; esac
done
if [ "$mode" = rotate ]; then
  test -x "$DIFFMIND_BINARY"
  test -d "$DIFFMIND_HOME/projects"
  test -d "$DIFFMIND_BACKUP_DIRECTORY"
fi

# This lock covers the entire stop/backup/restart sequence, not only rotation.
# Its parent must be root-owned and private; never remove a live lock file.
exec 9>"$DIFFMIND_BACKUP_LOCK"
flock -n 9 || { echo 'Another backup maintenance cycle owns the lock' >&2; exit 1; }
restart_marker="$DIFFMIND_BACKUP_LOCK.restart"
if [ -f "$restart_marker" ]; then
  recorded=$(cat "$restart_marker")
  test "$recorded" = "$DIFFMIND_SERVICE" || { echo 'Unresolved restart intent belongs to a different service; operator action required' >&2; exit 1; }
  systemctl start "$DIFFMIND_SERVICE"
  rm -- "$restart_marker"
fi
if [ "$mode" = recover ]; then exit 0; fi
loaded=$(systemctl show --property=LoadState --value "$DIFFMIND_SERVICE")
test "$loaded" = loaded || { echo 'Service is missing or unavailable' >&2; exit 1; }
state=$(systemctl show --property=ActiveState --value "$DIFFMIND_SERVICE")
case "$state" in active|inactive) ;; *) echo "Refusing maintenance while service is $state" >&2; exit 1 ;; esac

restart=no
backup_pid=''
finish() {
  result=$?
  trap - EXIT HUP INT TERM
  if [ -n "$backup_pid" ]; then
    kill -TERM "$backup_pid" 2>/dev/null || true
    wait "$backup_pid" 2>/dev/null || true
  fi
  if [ "$restart" = yes ]; then
    if ! systemctl start "$DIFFMIND_SERVICE"; then
      echo 'BACKUP RECOVERY ERROR: could not restart the original service; operator action required' >&2
      result=1
    else
      rm -- "$restart_marker" || result=1
    fi
  fi
  exit "$result"
}
trap finish EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

if [ "$state" = active ]; then
  # Record restart intent BEFORE stop: a failed stop may already have stopped it.
  restart=yes
  # Private runtime intent also lets systemd's ExecStopPost recover after this
  # shell is killed. Publish it before stopping the service, never afterward.
  printf '%s\n' "$DIFFMIND_SERVICE" > "$restart_marker"
  systemctl stop "$DIFFMIND_SERVICE"
fi
"$DIFFMIND_BINARY" backup rotate --offline --directory "$DIFFMIND_BACKUP_DIRECTORY" \
  --keep-last "$DIFFMIND_BACKUP_KEEP_LAST" --json &
backup_pid=$!
result=0
wait "$backup_pid" || result=$?
backup_pid=''
exit "$result"
