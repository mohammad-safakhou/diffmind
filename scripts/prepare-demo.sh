#!/bin/sh
# Prepare disposable, synthetic repositories; never modify a real workspace.
set -eu
if [ "$#" -ne 1 ]; then
  echo "Usage: sh scripts/prepare-demo.sh NEW_DIRECTORY" >&2
  exit 2
fi
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
demo_dir=$1
# mkdir (without -p) atomically refuses every existing destination.
umask 077
mkdir -- "$demo_dir"
demo_dir=$(CDPATH= cd -- "$demo_dir" && pwd)
mkdir "$demo_dir/repositories" "$demo_dir/workspace" "$demo_dir/workspace/projects"
for service in gateway catalog billing; do
  cp -R "$root/testdata/company/$service" "$demo_dir/repositories/$service"
  git -C "$demo_dir/repositories/$service" init -q -b master
  git -C "$demo_dir/repositories/$service" add .
  git -C "$demo_dir/repositories/$service" -c user.name='Demo Developer' \
    -c user.email='developer@example.test' -c commit.gpgsign=false commit -q -m 'Synthetic demo fixture'
done
printf '\nDemo prepared (synthetic source, no credentials):\n  %s\n' "$demo_dir"
printf 'Set DIFFMIND_HOME to:\n  %s/workspace\n' "$demo_dir"
printf 'Start ./bin/diffmind, create a project, and Import & build this local directory:\n  %s/repositories\n' "$demo_dir"
printf 'Expected: gateway calls catalog and billing; status.example.test remains external.\n'
