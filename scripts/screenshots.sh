#!/usr/bin/env bash
# Regenerates docs/screenshots/*.png (needs `freeze`: go install github.com/charmbracelet/freeze@latest)
set -euo pipefail
cd "$(dirname "$0")/.."
root=/tmp/hnt-demo # short, stable paths look nicer in the screenshots
rm -rf "$root"; mkdir -p "$root"
eval "$(scripts/sandbox.sh "$root/sandbox")"
export HNT_NO_UPDATE_CHECK=1
mkdir -p docs/screenshots
go run ./scripts/screenshots "$root/ans"
for f in "$root"/ans/*.ans; do
  name=$(basename "$f" .ans)
  timeout 60 freeze "$f" --language ansi --output "docs/screenshots/$name.png" \
    --window --padding 16 --margin 0 --border.radius 10 --background "#1e1f29" \
    --font.size 14 --line-height 1.25
done
# shrink the PNGs (resize + palette) so the repo stays light; needs Pillow
python3 scripts/compress_png.py docs/screenshots/*.png
rm -rf "$root"
