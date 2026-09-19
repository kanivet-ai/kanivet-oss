# Kanivet visual system — macOS native

Kanivet looks like a first-party macOS app: system font, vibrancy sidebar and
toolbar, hairline separators, grouped "settings-style" cards, blue selection,
segmented controls, round labelled actions. Both appearances (light/dark) are
first-class. This document is the contract every stylesheet follows.

Tokens live in `src/index.css`; reusable recipes in `src/styles/primitives.css`
(`.ap-*` classes). Reference implementations: `TabBar.css`, `TreeSidebar.css`,
`TreeNode.css`, `ResourceList.css`, `DetailView.css`, `BottomDock.css`,
`CommandPalette.css`, `common/Dialog.css`.

## 1. Rules

1. **No hard-coded colours.** Every colour is a `var(--…)`. Hex/rgb literals are
   allowed only for pure white/black text on solid blue/red/green fills
   (`#fff`), and inside `rgba(0,0,0,…)` shadows.
2. **No non-system fonts.** Never reference Inter, Instrument Serif, JetBrains
   Mono, Geist or `@import url(fonts…)`. Use `var(--font-sans)` (system UI) and
   `var(--font-mono)` (SF Mono). No serif display type anywhere.
3. **Hairlines, not borders.** Row/section separators are
   `1px solid var(--hair)`. Stronger dividers (pane edges, dock top) use
   `var(--sep)`. Cards and buttons get their edge from a 0.5px ring in
   `box-shadow` (`var(--shadow-card)` / `var(--shadow-pill)`), not `border`.
4. **Radii:** controls 6–7px, cards/menus 10px, sheets/palette 12–14px, pills
   6px, badges 5px, round actions 50%.
5. **Blue selection.** The one selected row in a table/list/sidebar is a solid
   `var(--sel)` fill with `var(--on-sel)` text (`var(--on-sel2)` for secondary
   text); hover is `var(--hover)`. A *tinted* selection (`var(--sel-soft)`) is
   only for items whose content has its own colours that must stay readable
   (charts, diff lines, code).
6. **Segmented controls** replace underline tabs: a `var(--ctrl)` track with a
   `var(--card)` + `var(--shadow-pill)` active segment. Open-document tabs are
   pills (`.ap-pill`), never underlined.
7. **Hierarchy through weight and alpha**, not size or caps: primary
   `var(--text)`, secondary `var(--text2)`, tertiary `var(--text3)`, disabled
   `var(--text4)`. Section labels are `11.5px/600 var(--text2)`, sentence case.
   No `text-transform: uppercase` and no wide letter-spacing on labels or
   badges.
8. **Numbers are tabular** (`font-variant-numeric: tabular-nums`). Identifiers
   (UIDs, images, namespaces in dense tables) may use `var(--font-mono)` at
   11–11.5px.
9. **Icons:** stroked, 1.8–2px, `currentColor`. Sidebar resource icons are
   `var(--blue)` (white when selected). Status uses 7px dots
   (`.ap-dot--success|warning|danger`) or 16px filled glyph circles.
10. **Motion:** 120–180ms, `var(--ease-quiet)`; popovers use `ap-pop`. Nothing
    bounces or blinks except the terminal cursor.
11. **Keep every selector that exists.** Restyle rules; never delete a selector
    or change a class name used by a TSX file. Layout properties (flex, grid,
    sizes that TSX measures — e.g. 30px table rows, 26px checkbox column,
    38px tab headers unless the header is restyled deliberately) stay put.
12. **Both themes always.** Never write `[data-theme='dark'] { … }` overrides
    with literals; the tokens already flip. Only add theme blocks for genuinely
    different treatment (e.g. ANSI log colours).

## 2. Surfaces

| Token | Use |
| --- | --- |
| `--content` (`--bg-primary`) | main content: tables, editors, log bodies, dialog body |
| `--win` (`--bg-secondary`) | window chrome: sidebar base, dock, status bar, inspector base, sheet |
| `--sidebar` + `.ap-vibrancy` | the tree sidebar |
| `--toolbar` + `.ap-vibrancy` | top title/tool bar, menus, tooltips |
| `--card` | grouped cards, active segment/pill, buttons |
| `--inset` | code blocks, nested detail areas, label chips background |
| `--hover` / `--stripe` | hover fill / zebra stripe (odd table rows) |
| `--ctrl` / `--ctrl2` | segmented tracks, search fields, neutral badge fill / scrollbar thumb |
| `--hair` / `--sep` | hairline / strong separator |
| `--sel` / `--on-sel` / `--on-sel2` | selection fill / text on it |
| `--blue --green --orange --red --purple --teal --yellow --gray` | semantic + chart colours |
| `--success-bg --warning-bg --danger-bg --blue-soft` | tinted badge fills |
| `--shadow-card --shadow-pill --shadow-pop --shadow-window` | card ring, active segment, menus/popovers, sheets |

Legacy names still resolve: `--text-primary/secondary/muted`, `--border`
(≈ hair, slightly stronger), `--accent`(blue), `--success/--warning/--danger`,
`--bg-tertiary` (one step above `--win`), `--bg-active` (neutral pressed),
`--bg-hover`.

## 3. Recipes (copy the values when you cannot add the class)

**Toolbar** — 52px tall for the main window bar, 36px for pane bars:
`background: var(--toolbar)` + vibrancy, `border-bottom: 1px solid var(--hair)`,
title `15px/600 letter-spacing:-0.2px`, subtitle `13px var(--text2)`.

**Sidebar row** — 28px, radius 6px, padding `0 8px 0 10px`, icon 15px blue,
label 13px, count `11.5px var(--text3)` tabular. Selected: `--sel` fill, white
text, white icon, `--on-sel2` count. Section header `11px/600 var(--text3)`,
padding `0 10px 4px`.

**Table** — header row 26px, `11.5px/500 var(--text2)`, cells separated by
`border-left: 1px solid var(--hair)`; body rows 30px, `13px` name, `12.5px
var(--text2)` other cells, odd rows `--stripe`, hover `--hover`, selected
`--sel` with all descendants forced to on-sel colours. Status = dot + text.
Sort arrow 9px stroked in header.

**Card group** — `.ap-group-label` above an `.ap-card` of `.ap-card-row`s
(`8px 14px`, 12.5px label left, `--text2` value right, hairline between).

**Segmented / pill / buttons / inputs / toggle / badges / menus / tooltip /
sheet** — see `.ap-segmented`, `.ap-pill`, `.ap-btn(--primary|--danger|--ghost)`,
`.ap-icon-btn`, `.ap-action(--warn|--danger)`, `.ap-input`, `.ap-search`,
`.ap-select`, `.ap-toggle`, `.ap-badge--*`, `.ap-menu`, `.ap-tooltip`,
`.ap-sheet` in `primitives.css`.

**Inspector (detail panel)** — header: 44px gradient tile + `15px/600` name +
`12px var(--text2)` "Kind · namespace · Running for 10h"; a row of
`.ap-action`s; a full-width `.ap-segmented`; then card groups. Cards are
`margin: 0 14px 14px`, sections have no uppercase titles.

**Console dock** — top edge `1px solid var(--sep)`, header 34px on `--win`
with an `.ap-segmented` (Logs / Terminal / Events) and an `.ap-search--sm`
filter, body on `--content`, `var(--font-mono) 11px/1.75`, timestamps
`--text3`, levels bold coloured (`info` blue, `warn` orange, `error` red).

**Command palette** — Spotlight: 640px, `--toolbar` + blur(50px) saturate(1.6),
radius 14px, `--shadow-pop`, 22px input, filter chips as 12px-radius pills,
results as 8px-radius rows with 30px 8px-radius icon tiles; highlighted result
= `--sel` fill with white text.

**Status footer** — `.ap-statusbar`: 24px, 11px `--text2`, centred, "10 pods ·
10 running · updated just now".

**Empty / error states** — centred, `13px var(--text2)`, an `.ap-btn` for the
action. No emoji, no italics.

## 4. Checklist before finishing a file

- `grep -nE '#[0-9a-fA-F]{3,8}\b' file.css` shows only `#fff`/shadows.
- No `Inter`, `JetBrains`, `Instrument`, `Geist`, `serif`, `@import url`.
- No `text-transform: uppercase` on labels/badges; `letter-spacing` ≤ 0.02em.
- Every selector that was in the file is still in the file.
- `npx tsc --noEmit` and `npx vite build` pass from `frontend/`.
