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

test -f "$asset_dir/public-proof.png"
test -f "$asset_dir/demo-shop-graph.jpg"
test -f "$asset_dir/graph-comparison.jpg"
test -f "$asset_dir/enterprise-overview.png"

ffmpeg -hide_banner -loglevel error -y \
  -loop 1 -t 4 -i "$asset_dir/public-proof.png" \
  -loop 1 -t 4 -i "$asset_dir/demo-shop-graph.jpg" \
  -loop 1 -t 4 -i "$asset_dir/graph-comparison.jpg" \
  -loop 1 -t 4 -i "$asset_dir/enterprise-overview.png" \
  -filter_complex '[0:v]scale=1200:540,pad=1200:675:0:67:color=0x070b14,setsar=1,fps=12[v0];[1:v]crop=1300:731:290:250,scale=1200:675,setsar=1,fps=12[v1];[2:v]crop=1280:720:330:0,scale=1200:675,setsar=1,fps=12[v2];[3:v]crop=1288:725:290:90,scale=1200:675,setsar=1,fps=12[v3];[v0][v1]xfade=transition=fade:duration=0.5:offset=3.5[x1];[x1][v2]xfade=transition=fade:duration=0.5:offset=7[x2];[x2][v3]xfade=transition=fade:duration=0.5:offset=10.5,split[s0][s1];[s0]palettegen=max_colors=192[p];[s1][p]paletteuse=dither=bayer' \
  -loop 0 "$output"

printf 'Rebuilt %s\n' "$output"
