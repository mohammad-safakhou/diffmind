#!/bin/sh
# Rebuild the README animation from already-sanitized browser captures.
set -eu

project_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
asset_dir=$project_root/docs/assets/readme
output=$asset_dir/diffmind-demo.gif

if ! command -v ffmpeg >/dev/null 2>&1; then
  echo 'ffmpeg is required to rebuild the demo animation' >&2
  exit 1
fi

for frame in project-list demo-shop-graph graph-comparison operations-history; do
  test -f "$asset_dir/$frame.jpg"
done

ffmpeg -hide_banner -loglevel error -y \
  -loop 1 -t 2.5 -i "$asset_dir/project-list.jpg" \
  -loop 1 -t 3 -i "$asset_dir/demo-shop-graph.jpg" \
  -loop 1 -t 2.5 -i "$asset_dir/graph-comparison.jpg" \
  -loop 1 -t 2 -i "$asset_dir/operations-history.jpg" \
  -filter_complex '[0:v]scale=1200:-1[v0];[1:v]scale=1200:-1[v1];[2:v]scale=1200:-1[v2];[3:v]scale=1200:-1[v3];[v0][v1][v2][v3]concat=n=4:v=1:a=0,fps=8,split[s0][s1];[s0]palettegen=max_colors=128[p];[s1][p]paletteuse=dither=bayer' \
  -loop 0 "$output"

printf 'Rebuilt %s\n' "$output"
