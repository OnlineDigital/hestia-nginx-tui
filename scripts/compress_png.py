#!/usr/bin/env python3
"""Shrinks screenshot PNGs in place: width <= 2000px, adaptive palette, optimized.
Terminal screenshots have very few distinct colours, so this is visually lossless
at README size and typically cuts 85-90% of the bytes.  Needs Pillow."""
import sys
from PIL import Image

MAX_W = 2000

for path in sys.argv[1:]:
    im = Image.open(path).convert("RGBA")
    if im.width > MAX_W:
        im = im.resize((MAX_W, round(im.height * MAX_W / im.width)), Image.LANCZOS)
    # keep the rounded window corners transparent
    bg = Image.new("RGBA", im.size, (0, 0, 0, 0))
    bg.alpha_composite(im)
    q = bg.quantize(colors=256, method=Image.Quantize.FASTOCTREE, dither=Image.Dither.NONE)
    q.save(path, optimize=True)
    print(f"{path}: {im.width}x{im.height}")
