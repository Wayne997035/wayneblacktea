# Logo design brief — 韋恩紅茶

## Name origin

**wayneblacktea** = 韋恩 + 紅茶. The project is intentionally named
after a daily ritual: a cup of black tea while thinking. The brand
should feel **悠閒** (unhurried, calm) — the opposite of the
hyper-productivity AI aesthetic.

## What the logo should convey

| Want | Don't want |
|---|---|
| Calm. Tea-shop, study-corner, late-afternoon mood. | Hustle. Rocketship, lightning, "AI for productivity". |
| Tactile. Steam, tea leaves, ceramic, paper. | Glassy. Crypto-glossy, tech-blue gradients. |
| A single mark that works at favicon size (32×32). | Anything that needs colour to read. |
| Friendly to a serif or warm sans wordmark next to it. | A logotype that is the entire identity. |

## Concrete direction

Two strong candidates — both feel like 紅茶悠閒 and read at small
sizes. Pick one (or commission both, then a/b):

### Option A — A teacup seen from above, with a 'w' formed by steam

A simple round cup outline, top view. Two stylised steam curls rise
from the cup to form a lowercase **w**. The 'w' should be obvious
at favicon size; the cup outline is decorative around it.

- Single colour: deep tea red (#8C2A1A or similar, roughly
  the colour of brewed Assam).
- Inverse for dark mode: cream (#F5EAD8) on black.
- Wordmark next to it: serif-leaning sans (Inter Display, IBM Plex
  Serif), the word **wayneblacktea** lowercase.

### Option B — A tea-leaf monogram

A single tea leaf with a soft curl. Inside the leaf negative space, a
tiny lowercase **w** is carved out. At favicon size only the **w**
remains legible; the leaf becomes a quiet background hint.

- Same palette.
- Wordmark below: smaller, looser tracking, maybe a hairline above
  it for the 紅茶 horizontal-line vibe.

## Palette

| Use | Hex | Notes |
|---|---|---|
| Primary mark (light bg) | `#8C2A1A` | Brewed Assam red. Strong but warm. |
| Primary mark (dark bg) | `#F5EAD8` | Cream / soft milk-tea. |
| Accent | `#3A2517` | Dark roast, used sparingly (e.g. underline). |
| Background (docs / dashboard hero) | `#FAF6EE` | Paper. |

Avoid `#FF0000`, `#000000`, and any blue.

## Deliverables

- `docs/logo.svg` — square, 1×, vector, single-colour. The README
  references this path.
- `docs/logo-dark.svg` — same mark, cream-on-transparent, for dark
  hero sections.
- `web/public/favicon.svg` — 32×32 square, the 'w'-only crop of
  whichever option won.

A separate wordmark file is **not** needed for v1; the README writes
the project name in markdown.

## Tools that match this aesthetic

- [Recraft.ai](https://www.recraft.ai/) — has a "minimal monogram"
  preset that matches this brief well; iterate on prompts until the
  cup-or-leaf shape is right, then export SVG.
- [Figma](https://figma.com) — best for the cup-shape variant, since
  steam curls forming a 'w' is easier to draw than to prompt.
- [Tabler Icons](https://tabler-icons.io/i/teapot) /
  [Phosphor Icons](https://phosphoricons.com/?q=tea) — the `teapot`
  / `coffee` / `leaf` glyphs make decent v0 placeholders if a real
  bespoke logo will take time.

## Until the real logo lands

`docs/logo-placeholder.svg` is checked in and referenced by
`README.md`. It's a small, license-free hand-drawn 'w' with steam,
specifically designed to be ugly enough that nobody mistakes it for
the final logo.
