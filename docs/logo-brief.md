# Logo design brief — 韋恩紅茶 / wayneblacktea

## Name origin

**wayneblacktea** = 韋恩 + 紅茶. The project is named after a daily
ritual: a cup of black tea while thinking. Brand mood is **悠閒** —
unhurried, slightly nostalgic, the opposite of the hyper-productivity
AI aesthetic.

## What the logo must do

1. **Read as wayneblacktea, not just "a tea logo"**. The wordmark has
   to be inside the mark — not a separate caption beside it. A plain
   teacup is too generic; cafes everywhere use one.
2. **Hold up at favicon size.** 32×32 pixels is the test.
3. **Single colour by default.** Colour is decoration, not load-bearing.
4. **Feel like a tea shop, not like a tech startup.** Tactile, paper,
   ceramic, steam — not glossy gradients.

## The chosen direction — *the teabag tag*

A simple teacup, line-drawn, with a **teabag tag dangling over the
rim**. The tag is a small rectangle of paper carrying the
**wayneblacktea** wordmark. The string that connects the tag to the
bag (inside the cup) doubles as a stem.

Why this works:

- The dangling tag is an unusual silhouette. Most cafe / tea logos
  show only the cup; the tag is what makes someone notice it.
- The wordmark is **part of the mark**, not a separate label. There
  is no version of this logo that displays without the brand name.
- Reads at favicon size: cup silhouette plus the small bright tag is
  recognisable even when you can't read the text.
- Anchors a real-world ritual (steeping a teabag) and a 1990s-style
  paper-tag aesthetic — both 悠閒.

### Variants to commission

- **Mark only** (`docs/logo.svg`) — the cup + tag, with the
  wordmark printed on the tag. The reader sees a complete logo.
- **Wordmark only** (`docs/wordmark.svg`) — *wayneblacktea* in the
  same typeface as the tag, no cup. For places where the mark would
  feel decorative (PDF footer, plain-text README header).
- **Favicon** (`web/public/favicon.svg`) — only the tag, cropped
  square. The cup is dropped at this size; the tag becomes the icon.
- **Dark-mode** versions of mark and wordmark in cream on
  transparent.

## Alternatives considered (rejected)

| Concept | Why not the chosen one |
|---|---|
| Plain teacup with steam curl | Generic — every cafe has this. No wordmark integration. |
| Loose-leaf bowl, leaves arranged into "w" | Cute but doesn't spell out the brand; favicon would just look like leaves. |
| Steam ribbon writing "wayneblacktea" in cursive | Distinctive but illegible at favicon size. |
| Tea-leaf monogram with carved-out 'w' | Same problem as the bowl — no wordmark when shrunk. |

## Palette

| Use | Hex | Notes |
|---|---|---|
| Mark stroke / tag print (light bg) | `#8C2A1A` | Brewed Assam red. The "tea" colour. |
| Mark stroke / tag print (dark bg) | `#F5EAD8` | Cream / soft milk-tea. |
| Tag paper | `#FAF6EE` (light bg) / `#3A2517` (dark bg) | Always reads as paper. |
| Accent (string, dotted lines) | `#3A2517` | Only the string and any decorative hairlines. |

Avoid `#FF0000`, `#000000`, blues, and any glossy gradient.

## Typography on the tag

- Wordmark on the tag is **lowercase**: `wayneblacktea`.
- Typeface: a slightly serifed display sans (Inter Display, IBM Plex
  Sans, or Söhne) at a heavy weight so it survives small sizes. A
  pure serif will get lossy at 32 px.
- Tracking very slightly loose (~0.04 em) — the tag should feel
  printed, not crammed.
- A secondary line below the wordmark on the tag (optional, only on
  large sizes): `韋恩紅茶` in the same sans, lighter weight. Drops
  silently when the mark scales down.

## Deliverables

- `docs/logo.svg` (and `logo-dark.svg`) — vector, single-colour,
  square viewbox.
- `docs/wordmark.svg` (and `-dark.svg`) — wordmark only.
- `web/public/favicon.svg` — square favicon, the tag only.
- High-res PNG fallbacks for places SVG is awkward (256, 512, 1024).

## Tools

- [Recraft.ai](https://www.recraft.ai/) — describe "minimalist
  line-drawn teacup with a teabag tag hanging over the rim, the tag
  carries the lowercase wordmark wayneblacktea, paper-print
  aesthetic, single deep-red colour, calm and unhurried mood".
  Iterate. Export SVG.
- [Figma](https://figma.com) — best for hand-drawing the cup +
  string + tag rectangle and placing the wordmark inside.
- [Phosphor Icons](https://phosphoricons.com/?q=tea) — `teapot` /
  `coffee` / `leaf` glyphs as v0 placeholders if a real bespoke
  logo will take time.

## Until the real logo lands

`docs/logo-placeholder.svg` is checked in and referenced by the
README. It already implements this direction (cup + tag with
wordmark) so the README looks intentionally designed even before the
real deliverable arrives. The placeholder is deliberately rough so
nobody mistakes it for the final mark.
