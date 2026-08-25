#!/bin/sh
# Renders the demo animations from packages/docs/demo/illustration and writes
# the committed assets in imgs/: a gif of the full story for the repository
# README, a gif of the blast-radius cut for the commit-messages page, and a
# webm/mp4 pair per key-feature illustration for the landing page's carousel.
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

for comp in Master Blast BlastClip Order Heal Control Polyglot Terminal Compute Run Single Hooks Polyrepo Lock Glue Math Why; do
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

# One video pair per key-feature illustration for the landing page carousel.
# The composition-to-asset list here must match FEATURE_MEDIA in
# packages/docs/src/components/DemoCarousel/index.tsx, which is what plays
# them.
for pair in Order:demo-order BlastClip:demo-blast Heal:demo-heal Control:demo-control Polyglot:demo-polyglot Terminal:demo-terminal Compute:demo-compute Run:demo-run Single:demo-single Hooks:demo-hooks Polyrepo:demo-polyrepo Lock:demo-lock Glue:demo-glue Math:demo-math Why:demo-why; do
  comp="${pair%%:*}"
  asset="${pair#*:}"
  ffmpeg -y -loglevel error -i "$out/$comp.mp4" -c:v libvpx-vp9 -b:v 0 -crf 40 -an "$root/imgs/$asset.webm"
  cp "$out/$comp.mp4" "$root/imgs/$asset.mp4"
done

# The committed gif budget, in bytes: the README embed is camo-cached by
# GitHub and deserves to stay small.
size="$(wc -c <"$root/imgs/demo-release.gif" | tr -d ' ')"
if [ "$size" -gt 2621440 ]; then
  echo "imgs/demo-release.gif is $size bytes, over the 2621440 budget" >&2
  exit 1
fi

ls -lh "$root"/imgs/demo-*
