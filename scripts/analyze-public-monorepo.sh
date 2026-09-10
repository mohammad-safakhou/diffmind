#!/bin/sh
# Analyze each src/* target independently, then analyze the monorepo root.
set -eu
if [ "$#" -ne 2 ]; then
  echo "Usage: sh scripts/analyze-public-monorepo.sh REPOSITORY NEW_OUTPUT_DIRECTORY" >&2
  exit 2
fi

project_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
repository=$(CDPATH= cd -- "$1" && pwd)
output=$2
test -x "$project_root/bin/diffmind"
test -d "$repository/.git"
test -d "$repository/src"
mkdir -- "$output"
output=$(CDPATH= cd -- "$output" && pwd)
mkdir "$output/services" "$output/together" "$output/workspace"

for source in "$repository"/src/*; do
  test -d "$source" || continue
  service=${source##*/}
  DIFFMIND_HOME=$output/workspace "$project_root/bin/diffmind" run \
    --repo "$source" --out "$output/services/$service"
done

DIFFMIND_HOME=$output/workspace "$project_root/bin/diffmind" run \
  --repo "$repository" --out "$output/together"
printf 'Per-service and whole-monorepo analysis written to %s\n' "$output"
