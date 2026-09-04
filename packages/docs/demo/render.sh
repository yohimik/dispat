#!/bin/sh
# Renders the demo animations from packages/docs/demo/illustration and writes
# the committed assets in imgs/: a gif of the full story for the repository
# README and a gif of the blast-radius cut for the commit-messages page. The
# landing page imports these scene components into Remotion Player directly.
# Requires node with pnpm and ffmpeg with gifsicle (brew install ffmpeg
# gifsicle).
set -eu

root="$(cd "$(dirname "$0")/../../.." && pwd)"
ill="$root/packages/docs/demo/illustration"
out="$ill/out"

command -v ffmpeg >/dev/null || { echo "ffmpeg is not installed (brew install ffmpeg)" >&2; exit 1; }
command -v gifsicle >/dev/null || { echo "gifsicle is not installed (brew install gifsicle)" >&2; exit 1; }

cd "$ill"
[ -d node_modules ] || pnpm install

for comp in Master Blast; do
  npx remotion render src/index.ts "$comp" "$out/$comp.mp4"
done

# The two gif embeds go through a generated palette: the README plays the
# whole story, and the commit-messages page plays the blast-radius cut beside
# the prose it illustrates. fps=10 against the compositions' 20, so a gif
# carries the same frame count (and so the same weight) as it did when the
# compositions ran at 30 and the filter took 15.
for pair in Master:demo-release Blast:demo-blast; do
  comp="${pair%%:*}"
  asset="${pair#*:}"
  ffmpeg -y -loglevel error -i "$out/$comp.mp4" \
    -vf "fps=10,scale=1200:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128[p];[b][p]paletteuse=dither=bayer:bayer_scale=4" \
    "$root/imgs/$asset.gif"
  gifsicle -b -O3 --lossy=40 "$root/imgs/$asset.gif"
done

# The committed gif budget, in bytes: the README embed is camo-cached by
# GitHub and deserves to stay small.
size="$(wc -c <"$root/imgs/demo-release.gif" | tr -d ' ')"
if [ "$size" -gt 2621440 ]; then
  echo "imgs/demo-release.gif is $size bytes, over the 2621440 budget" >&2
  exit 1
fi

ls -lh "$root"/imgs/demo-*.gif
