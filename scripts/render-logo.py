#!/usr/bin/env python3
"""Renders every raster logo from the geometry imgs/logo.svg declares.

The mark lives on a 9-unit grid: an outlined square of side 6 with a 1-unit
stroke, and a filled square of side 6 offset by 3. Every raster here is drawn
as axis-aligned rectangles at integer pixel coordinates, so no resampling ever
happens and every edge stays a strict line; this replaces the old sips-resize
recipe, whose fractional scales blurred the edges and drifted the squares.

Standard library only, on purpose: solid rectangles need no imaging library,
and the release toolchain gains no dependency. Run from the repository root:

    python3 scripts/render-logo.py
"""

import struct
import zlib

# Two darks, as imgs/logo.svg declares them: the ring sits a step lighter
# than the square, which keeps the overlap readable where the shapes meet.
RING = (23, 23, 23, 255)
SQUARE = (13, 13, 13, 255)
WHITE = (255, 255, 255, 255)
CLEAR = (0, 0, 0, 0)


def glyph_rects(u, ox, oy):
    """The five rectangles of the mark, in pixels, for unit u at offset ox, oy."""
    return [
        (ox + 0 * u, oy + 0 * u, 6 * u, 1 * u, RING),    # ring, top
        (ox + 0 * u, oy + 5 * u, 6 * u, 1 * u, RING),    # ring, bottom
        (ox + 0 * u, oy + 1 * u, 1 * u, 4 * u, RING),    # ring, left
        (ox + 5 * u, oy + 1 * u, 1 * u, 4 * u, RING),    # ring, right
        (ox + 3 * u, oy + 3 * u, 6 * u, 6 * u, SQUARE),  # filled square
    ]


def render(size, unit, background):
    """One square canvas with the mark centred; margins split odd pixels."""
    art = 9 * unit
    ox = (size - art) // 2
    oy = (size - art) // 2
    px = [[background] * size for _ in range(size)]
    for x, y, w, h, color in glyph_rects(unit, ox, oy):
        for yy in range(y, y + h):
            row = px[yy]
            for xx in range(x, x + w):
                row[xx] = color
    return px


def png_bytes(px):
    size = len(px)
    raw = b"".join(b"\x00" + b"".join(bytes(c) for c in row) for row in px)

    def chunk(tag, data):
        body = tag + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body))

    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0))
        + chunk(b"IDAT", zlib.compress(raw, 9))
        + chunk(b"IEND", b"")
    )


def ico_bytes(pngs):
    """An ICO wrapping already-rendered PNG images (PNG-in-ICO, universal now)."""
    out = struct.pack("<HHH", 0, 1, len(pngs))
    offset = 6 + 16 * len(pngs)
    entries, blobs = b"", b""
    for size, blob in pngs:
        entries += struct.pack(
            "<BBBBHHII", size % 256, size % 256, 0, 0, 1, 32, len(blob), offset
        )
        blobs += blob
        offset += len(blob)
    return out + entries + blobs


def write(path, data):
    with open(path, "wb") as f:
        f.write(data)
    print(f"wrote {path} ({len(data)} bytes)")


# The white-plate mark: the repository README and the social card, where a
# transparent background would land on unknown colours.
write("imgs/logo.png", png_bytes(render(300, 30, WHITE)))

# PWA and platform icons, flattened onto white: iOS composites alpha onto
# black and the Android splash draws over background_color, so dark art on
# transparency would vanish in both.
write("packages/docs/static/img/icon-192.png", png_bytes(render(192, 18, WHITE)))
write("packages/docs/static/img/icon-512.png", png_bytes(render(512, 48, WHITE)))
# Maskable: the safe zone is the centred circle at 80% of the canvas, and the
# largest inscribed square has side 409.6/sqrt(2) = 289; unit 32 keeps the
# 288-pixel art inside it on the grid.
write("packages/docs/static/img/icon-maskable-512.png", png_bytes(render(512, 32, WHITE)))
write("packages/docs/static/img/apple-touch-icon.png", png_bytes(render(180, 16, WHITE)))

# The legacy-favicon fallback, transparent like the SVG that modern browsers
# pick instead.
write(
    "packages/docs/static/favicon.ico",
    ico_bytes([
        (32, png_bytes(render(32, 3, CLEAR))),
        (48, png_bytes(render(48, 5, CLEAR))),
    ]),
)
