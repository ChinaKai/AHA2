"""Rasterizes the AHA mark used by the Web UI, Windows icon and tray.

The authoritative artwork is assets/aha-logo.svg (64px viewBox): a navy
rounded tile, a white double-A stroke and a cyan crossbar. This script mirrors
that geometry with Pillow so the PNG/ICO assets can be regenerated without an
SVG rasterizer. Keep the numbers in sync with the SVG when either changes.

Run `python3 assets/make-logo.py` to rewrite assets/aha2-logo.png and
assets/aha2.ico.
"""
from PIL import Image, ImageDraw

OUT = "assets"
NAVY = (15, 23, 42, 255)    # #0f172a
WHITE = (248, 250, 252, 255)  # #f8fafc
CYAN = (56, 189, 248, 255)  # #38bdf8
SIZES = [16, 20, 24, 32, 40, 48, 64, 128, 256]
POINTS = [(12, 48), (22, 17), (32, 48), (42, 17), (52, 48)]


def draw_mark(S):
    """Draw at resolution S using the same 64px viewBox as aha-logo.svg."""
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    scale = S / 64

    d.rounded_rectangle([0, 0, S - 1, S - 1], radius=S * 14 / 64, fill=NAVY)

    points = [(x * scale, y * scale) for x, y in POINTS]
    stroke = 5.2 * scale
    d.line(points, fill=WHITE, width=max(1, round(stroke)), joint="curve")
    radius = stroke / 2
    for x, y in points:
        d.ellipse([x - radius, y - radius, x + radius, y + radius], fill=WHITE)

    cross = 4.8 * scale
    cross_radius = cross / 2
    y = 35.5 * scale
    d.line([(16 * scale, y), (48 * scale, y)], fill=CYAN, width=max(1, round(cross)))
    for x in (16 * scale, 48 * scale):
        d.ellipse([x - cross_radius, y - cross_radius, x + cross_radius, y + cross_radius], fill=CYAN)
    return img


def main():
    base = draw_mark(2048).resize((1024, 1024), Image.LANCZOS)
    base.save(f"{OUT}/aha2-logo.png")
    # Build each size by drawing at 4x then downscaling, so 16px keeps clean
    # edges instead of being a squash of the 256px frame.
    frames = {s: draw_mark(s * 4).resize((s, s), Image.LANCZOS) for s in SIZES}
    frames[256].save(f"{OUT}/aha2.ico", format="ICO", sizes=[(s, s) for s in SIZES])
    print("wrote", f"{OUT}/aha2.ico", "and", f"{OUT}/aha2-logo.png")


if __name__ == "__main__":
    main()
