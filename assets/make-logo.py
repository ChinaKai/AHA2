"""Draws the AHA2 mark: a bold geometric "A" on the app's brand-blue tile.

Kept as source so the icon can be regenerated rather than only existing as a
binary blob. Run `python3 assets/make-logo.py` to rewrite assets/aha2.ico and
assets/aha2-logo.png.
"""
from PIL import Image, ImageDraw

OUT = "assets"
BLUE = (29, 78, 216, 255)   # #1d4ed8, the brand-mark colour in styles.css
WHITE = (255, 255, 255, 255)
SIZES = [16, 20, 24, 32, 40, 48, 64, 128, 256]


def draw_mark(S):
    """Draw at resolution S. Shapes are filled polygons, not wide lines, so there
    are no line-join bulges at the apex or the feet."""
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    d.rounded_rectangle([0, 0, S - 1, S - 1], radius=S * 0.22, fill=BLUE)

    # The "A" as a triangle ring: outer triangle minus an inner triangle, using
    # even-odd fill, plus a crossbar. Geometry is defined as fractions of S so
    # every size is the same drawing rather than a separate approximation.
    apex_y, foot_y = 0.215, 0.775
    half_foot = 0.315
    outer = [
        (0.5, apex_y),
        (0.5 + half_foot, foot_y),
        (0.5 - half_foot, foot_y),
    ]
    bar_top, bar_bottom = 0.615, 0.690
    # Inner triangle: same apex angle, inset by half the stroke thickness.
    stroke_v = 0.095                       # vertical edge thickness at the feet
    inner_scale = (foot_y - apex_y - stroke_v) / (foot_y - apex_y)
    inner_half = half_foot * inner_scale
    inner_apex_y = apex_y + stroke_v
    inner = [
        (0.5, inner_apex_y),
        (0.5 + inner_half, foot_y),
        (0.5 - inner_half, foot_y),
    ]
    # The crossbar spans the ring at its widest, then the inner triangle is
    # subtracted from it so the bar reads as part of the letter, not a slab.
    t_top = (bar_top - apex_y) / (foot_y - apex_y)
    t_bottom = (bar_bottom - apex_y) / (foot_y - apex_y)
    bar_half_top = half_foot * t_top
    bar_half_bottom = half_foot * t_bottom
    bar_outer = [
        (0.5 - bar_half_bottom, bar_bottom),
        (0.5 + bar_half_bottom, bar_bottom),
        (0.5 + bar_half_top, bar_top),
        (0.5 - bar_half_top, bar_top),
    ]

    def pts(poly):
        return [(x * S, y * S) for x, y in poly]

    d.polygon(pts(outer), fill=WHITE)
    d.polygon(pts(inner), fill=BLUE)
    d.polygon(pts(bar_outer), fill=WHITE)
    return img


def main():
    base = draw_mark(1024)
    base.save(f"{OUT}/aha2-logo.png")
    # Build each size by drawing at that size's 4x then downscaling, so 16px keeps
    # clean edges instead of being a squash of a large bitmap.
    frames = {s: draw_mark(s * 4).resize((s, s), Image.LANCZOS) for s in SIZES}
    frames[256].save(f"{OUT}/aha2.ico", format="ICO", sizes=[(s, s) for s in SIZES])
    print("wrote", f"{OUT}/aha2.ico", "and", f"{OUT}/aha2-logo.png")


if __name__ == "__main__":
    main()
