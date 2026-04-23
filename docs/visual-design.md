# 🎨 Visual Design — Kestrel

> **OBSOLETE (2026-04-23).** Kestrel now ships daisyUI v5 on top of Tailwind v4. The bespoke neo-skeuomorphic token system described below has been removed; theming is whichever daisyUI built-in theme the user selects via the in-app `ThemeController`. See `frontend/src/shell/app.css` for the enabled theme list. The rest of this file is kept as historical reference only and must not be used to guide new work.

---

> This document defines the visual language for Kestrel's frontend: design tokens, the tactile elevation system, per-component specs, and implementation rules.
> It complements [`ui-design.md`](ui-design.md) (which owns architecture, transport, and component hierarchy) and is referenced by [`CLAUDE.md`](../CLAUDE.md) and [`.github/copilot-instructions.md`](../.github/copilot-instructions.md).

---

## Overview & Design Intent

Kestrel uses a **dark neo-skeuomorphic tactile** visual system: near-black surfaces catch soft light from above, interactive controls sit physically raised off the background, active elements glow in a warm orange accent, and pressed states depress into the surface. The metaphor is a real panel of physical controls — every button is something you can feel.

**Why it fits a photo manager.** The app's job is to show photographs. The chrome must stay recessive — dark, matte, low-contrast — so the content carries the eye. When the user reaches for a control, the tactile elevation makes the affordance obvious without shouting. The orange accent is reserved for state that the user cares about: the active tab, the current step, the primary action, the progress they're making through a scan.

**Dark-mode only.** Light mode is not on the roadmap.

---

## Design Tokens

All tokens live in `frontend/src/shell/tokens.css` and are imported by the shell. Components reference `var(--token)` — never hard-coded values.

```css
:root {
  /* ─── Color ──────────────────────────────────────────────────── */
  --bg:                #0E0E10;   /* app background */
  --surface-raised:    #1A1A1D;   /* raised panels, cards, buttons */
  --surface-inset:     #0A0A0C;   /* inset wells: text fields, tracks */
  --surface-pressed:   #141416;   /* button pressed-in state */
  --border-subtle:     #2A2A2E;   /* hairline dividers */

  --text-primary:      #EDEDED;
  --text-secondary:    #B8B8BC;
  --text-muted:        #6E6E74;

  --accent:            #E8783C;   /* signature orange */
  --accent-hover:      #F08A4E;
  --accent-pressed:    #C8662E;
  --accent-glow:       rgba(232, 120, 60, 0.45);

  --danger:            #D84B4B;
  --warning:           #E0A02A;
  --success:           #4BB26E;

  /* ─── Elevation (the tactile core) ───────────────────────────── */
  /* Convention: outer dark drop (below-right) + top-edge light highlight.
     Invert for inset; compress for pressed. See "Tactile Shadow Recipe". */
  --elev-base:    none;

  --elev-raised:
    0 3px 6px rgba(0, 0, 0, 0.55),
    0 1px 0 rgba(255, 255, 255, 0.04) inset,
    0 -1px 0 rgba(0, 0, 0, 0.4) inset;

  --elev-inset:
    0 2px 4px rgba(0, 0, 0, 0.6) inset,
    0 -1px 0 rgba(255, 255, 255, 0.03) inset;

  --elev-pressed:
    0 1px 2px rgba(0, 0, 0, 0.5) inset,
    0 1px 0 rgba(255, 255, 255, 0.02);

  --elev-overlay:    /* L4 — modals, popovers */
    0 10px 24px rgba(0, 0, 0, 0.7),
    0 2px 0 rgba(255, 255, 255, 0.05) inset;

  --elev-highest:    /* L5 — toasts, tooltips above overlays */
    0 14px 32px rgba(0, 0, 0, 0.75),
    0 2px 0 rgba(255, 255, 255, 0.06) inset;

  /* ─── Radius ─────────────────────────────────────────────────── */
  --radius-sm:   4px;
  --radius-md:   8px;
  --radius-lg:  16px;
  --radius-full: 9999px;          /* true pill */

  /* ─── Border thickness ───────────────────────────────────────── */
  --border-thin:  1px;
  --border-med:   3px;
  --border-thick: 5px;

  /* ─── Spacing (2px base) ─────────────────────────────────────── */
  --space-1:  2px;
  --space-2:  4px;
  --space-3:  8px;
  --space-4: 12px;
  --space-5: 16px;
  --space-6: 20px;
  --space-7: 24px;
  --space-8: 32px;
  --space-9: 48px;

  /* ─── Typography ─────────────────────────────────────────────── */
  --font-sans:
    ui-sans-serif, system-ui, -apple-system, "Inter", "Segoe UI",
    "Helvetica Neue", sans-serif;
  --font-mono:
    ui-monospace, "JetBrains Mono", "SF Mono", Menlo, Consolas, monospace;

  --fw-regular: 400;
  --fw-medium:  500;
  --fw-bold:    700;

  --fs-caption: 11px;
  --fs-body:    13px;
  --fs-default: 14px;
  --fs-subhead: 16px;
  --fs-heading: 20px;
  --fs-display: 28px;

  --tracking-label: 0.12em;       /* uppercase section labels */

  /* ─── Motion ─────────────────────────────────────────────────── */
  --dur-fast:  80ms;
  --dur-base: 160ms;
  --dur-slow: 240ms;
  --ease-out: cubic-bezier(0.2, 0.8, 0.2, 1);
}
```

### Token reference

| Group       | Token                       | Use                                                         |
| ----------- | --------------------------- | ----------------------------------------------------------- |
| Color       | `--bg`                      | App background, behind everything                           |
|             | `--surface-raised`          | Cards, buttons, toolbar, any panel that sits above `--bg`   |
|             | `--surface-inset`           | Wells: text field backgrounds, slider tracks, progress bars |
|             | `--surface-pressed`         | Momentary state for a button being held                     |
|             | `--accent` / `--accent-*`   | Interactive highlight; reserve for state, not decoration    |
|             | `--accent-glow`             | `box-shadow` color for active/focused glow                  |
| Elevation   | `--elev-base`               | Level 1 — subtle lift                                       |
|             | `--elev-raised`             | Level 2 — cards, buttons at rest                            |
|             | `--elev-inset`              | Level 3 — wells, pressed-into surfaces                      |
|             | `--elev-pressed`            | Level 3b — button held                                      |
|             | `--elev-overlay`            | Level 4 — modals, popovers                                  |
|             | `--elev-highest`            | Level 5 — toasts, tooltips                                  |
| Radius      | `--radius-sm/md/lg/full`    | 4 / 8 / 16 / pill                                           |
| Border      | `--border-thin/med/thick`   | 1 / 3 / 5 px                                                |
| Spacing     | `--space-1` … `--space-9`   | 2 / 4 / 8 / 12 / 16 / 20 / 24 / 32 / 48 px                  |
| Typography  | `--fs-*`                    | 11 / 13 / 14 / 16 / 20 / 28 px                              |

> **Note on spacing.** The reference styleguide showed in-between values (6, 10, 14, 28, 35). Round to the closest step — the 2px base is coherent across the system and the visual delta is imperceptible.

### Iconography

Three stroke-weighted sizes, per the reference board:

| Size | Stroke | Use                                        |
| ---- | ------ | ------------------------------------------ |
| 16px | 1.5    | Inline controls, list-item affordances     |
| 22px | 2.0    | Toolbar, sidebar nav, default button icons |
| 26px | 2.5    | Feature actions, empty-state illustrations |

Use a static icon library bundled at build time (Lucide or Phosphor). No icon fonts, no runtime SVG fetches.

---

## Tactile Shadow Recipe

Every surface on the board reads as physical because the shadow stack encodes a consistent light source: **light from top, shadow below-right**. Read each level as a combination of four possible layers:

1. **Outer drop** — `0 Ypx Bpx rgba(0,0,0,α)` below-right, sells "lifted off the surface".
2. **Top highlight** — `0 1px 0 rgba(255,255,255,α) inset`, sells "light catches the top edge".
3. **Inner shadow** — `0 Ypx Bpx rgba(0,0,0,α) inset`, sells "recessed into the surface".
4. **Bottom seam** — `0 -1px 0 rgba(0,0,0,α) inset`, sells "the edge rolls under".

**Raised** = outer drop + top highlight + bottom seam.
**Inset** = inner shadow + faint top highlight (the rim).
**Pressed** = compressed inner shadow + a `transform: translateY(1px)`.
**Overlay/highest** = deeper outer drop + top highlight, no seam (it floats free).

Do not hand-write these stacks per component. Compose with `var(--elev-*)`. If you find yourself writing `box-shadow: 0 3px 6px …` inline, you are off-system — reach for a variable.

---

## Component Specs

Every component on the reference board, in board order. Each subsection lists **purpose**, **states**, **tokens**, and a minimal CSS sketch.

### Buttons

Primary action. Orange raised pill with an inset top highlight that reads like a lit edge.

**States:** default, hover (lighter accent), pressed (pushes in + `translateY`), disabled (desaturated, no shadow), loading (spinner replaces label).

**Tokens:** `--accent`, `--accent-hover`, `--accent-pressed`, `--elev-raised`, `--elev-pressed`, `--radius-full`, `--space-4`/`--space-6`, `--fs-default`, `--fw-medium`.

```css
.btn {
  background: var(--accent);
  color: #fff;
  border: none;
  border-radius: var(--radius-full);
  padding: var(--space-4) var(--space-6);
  font: var(--fw-medium) var(--fs-default) / 1 var(--font-sans);
  box-shadow: var(--elev-raised);
  transition: box-shadow var(--dur-fast) var(--ease-out),
              transform var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.btn:hover    { background: var(--accent-hover); }
.btn:active   { background: var(--accent-pressed);
                box-shadow: var(--elev-pressed);
                transform: translateY(1px); }
.btn:disabled { background: #4A3A30; color: var(--text-muted);
                box-shadow: none; cursor: not-allowed; }
```

### Text Fields

Inset well, thin border, caret and label in the accent when focused.

**States:** empty (placeholder in `--text-muted`), default, focused (accent underline `--border-med`), error (`--danger` hairline + helper text below), disabled.

```css
.field {
  background: var(--surface-inset);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: var(--space-3) var(--space-4);
  color: var(--text-primary);
  box-shadow: var(--elev-inset);
}
.field:focus  { border-bottom: var(--border-med) solid var(--accent); outline: none; }
.field.error  { border-color: var(--danger); }
```

### Checkbox / Radio / Switch

All three share the tactile inset track. The switch thumb is a raised orange disc with `--accent-glow` when on.

- **Checkbox** — square, `--radius-sm`; checked = accent fill with white check icon (16px, stroke 2).
- **Radio** — circle, inset dot grows on select.
- **Switch** — pill track (`--elev-inset`), raised thumb (`--elev-raised`); on = thumb right + track tinted accent + `box-shadow: 0 0 12px var(--accent-glow)`.

### Slider

Inset track, raised orange thumb, optional floating label bubble anchored to the thumb.

```css
.slider-track { background: var(--surface-inset); box-shadow: var(--elev-inset);
                height: 6px; border-radius: var(--radius-full); }
.slider-fill  { background: var(--accent); border-radius: inherit; }
.slider-thumb { width: 18px; height: 18px; border-radius: var(--radius-full);
                background: var(--accent); box-shadow: var(--elev-raised),
                            0 0 10px var(--accent-glow); }
```

### Tabs

Raised pill group. The active tab fills with `--accent`; inactive tabs are transparent with `--text-secondary` labels.

### Breadcrumbs

Plain text with `›` separators (`--text-muted`). Trailing segment (current page) uses `--text-primary`; ancestors use `--text-muted`. No shadows.

### Stepper (multi-step process)

Circular nodes connected by a thin line. Three visual states:

- **Completed** — solid `--accent` fill, white check icon.
- **Active** — accent ring (`--border-med`), dark center, `box-shadow: 0 0 14px var(--accent-glow)`.
- **Upcoming** — `--elev-inset` circle, `--text-muted` numeral.

### Pagination

Circular buttons (`--radius-full`). Active page uses `--accent` fill with glow, inactive uses `--surface-raised` with `--elev-raised`. Previous/Next are pill-shaped with a chevron icon.

### Cards

Primary surface for grouped content.

```css
.card {
  background: var(--surface-raised);
  border-radius: var(--radius-lg);
  box-shadow: var(--elev-raised);
  padding: var(--space-5);
}
```

### List Items

Rows live inside an inset container; each row is subtly inset and lifts on hover.

```css
.list         { background: var(--surface-inset); box-shadow: var(--elev-inset);
                border-radius: var(--radius-md); padding: var(--space-2); }
.list-item    { padding: var(--space-3) var(--space-4);
                border-radius: var(--radius-md);
                transition: box-shadow var(--dur-fast) var(--ease-out); }
.list-item + .list-item { border-top: var(--border-thin) solid var(--border-subtle); }
.list-item:hover { background: var(--surface-raised); box-shadow: var(--elev-raised); }
```

### Badges

Small pill, `--fs-caption`, uppercase with `--tracking-label`.

- **Solid accent** (`New`, `Sale`) — `background: var(--accent)`, `color: #fff`.
- **Outlined accent** (`Provider`) — `border: var(--border-thin) solid var(--accent)`, `color: var(--accent)`.
- **Muted outline** (`Unassigned`) — `border-color: var(--border-subtle)`, `color: var(--text-muted)`.

### Avatars

Circular, `--radius-full`. Current user gets a `--border-med` `--accent` ring. Stacked avatars overlap with a 2px `--bg` outline for separation.

### Tooltip

Small raised bubble anchored above its trigger.

```css
.tooltip {
  background: var(--surface-raised);
  color: var(--text-primary);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  box-shadow: var(--elev-highest);
  font-size: var(--fs-body);
}
```

### Compact Table Header

Inset bar across the top of a table. Each header cell is a left-aligned label with a trailing sort chevron; active sort column shows `--accent` chevron.

### Modal Confirmation

Overlay (`--elev-overlay`) panel centered on a scrim (`rgba(0,0,0,0.6)`). Title in `--fs-heading`, body in `--fs-body`, two raised buttons in the footer. Destructive action uses `--danger` as the button fill.

### Toast Stack

Raised cards stacked top-right, slide in with `transform: translateX(100%)` → `0` over `--dur-base`. Each toast carries `--elev-highest` and auto-dismisses after 4s (respect `prefers-reduced-motion` and pause on hover).

### Alert Banner

Full-width raised bar across the top of a region. Leading warning icon (22px), message in `--text-primary`, background tinted with a low-alpha `--warning`.

### Empty State

Centered block: 2-color line illustration (monochrome + accent highlight), a short `--fs-subhead` message, and a single raised CTA button.

### Skeleton Rows

Inset pill shapes (`--elev-inset`, `--radius-full`) at body height. Shimmer is a slow linear-gradient sweep (`--dur-slow` × 6), **disabled** under `prefers-reduced-motion: reduce`.

### Linear Progress

Inset track with an orange fill; the leading edge carries a faint `box-shadow: 0 0 8px var(--accent-glow)`. Indeterminate variant sweeps a 30% segment across the track.

### Spinner

Orange 3/4 arc, `--dur-slow` infinite rotation. For long-running operations, prefer a gear-style icon that visually reads as "the app is working" rather than "the network is slow".

---

## Applying the System to Kestrel's Islands

| Island         | Surface               | Notes                                                                                              |
| -------------- | --------------------- | -------------------------------------------------------------------------------------------------- |
| `Sidebar`      | `--surface-inset`     | Folder tree rows are list items that lift to `--elev-raised` on hover; selected row gets accent.   |
| `Toolbar`      | `--surface-raised`    | Sort / view toggles are a raised pill group (Tabs pattern). Search is a text field, not a button.  |
| `PhotoGrid`    | `--bg` (no chrome)    | Photos are the content. Thumbnails sit on bare background with `--elev-base`. Selection = accent ring. |
| `PhotoCard`    | `--bg`                | On hover, lift to `--elev-raised`; overlay metadata fades in from `--text-muted` to `--text-primary`. |
| `PhotoViewer`  | `--bg` (full-bleed)   | Floating control cluster at the bottom uses `--elev-overlay`. No chrome competes with the image.  |
| `StatusBar`    | `--surface-inset`     | Inset strip with linear progress for scans; photo count + memory usage in `--text-secondary`.      |

**Rule of thumb:** chrome is recessive. The brightest thing on screen should almost always be a photo — or, if not, the one control the user is about to touch.

---

## Accessibility

- **Contrast.** `--text-primary` on `--bg` exceeds 7:1 (AAA body). `--text-secondary` on `--bg` meets 4.5:1 (AA). `--accent` on `--bg` meets 4.5:1 for interactive labels; do not use `--accent` for pure decoration where it might be mistaken for an affordance.
- **Focus ring.** Every interactive element keeps a visible focus ring: `outline: 2px solid var(--accent); outline-offset: 2px`. Never set `outline: none` without an equivalent replacement.
- **Reduced motion.** Under `@media (prefers-reduced-motion: reduce)`, disable the press `translateY`, skeleton shimmer, toast slide-in, and spinner rotation (use an opacity pulse instead).
- **Hit targets.** Minimum 32×32 px for any click/tap target; pagination and icon buttons use 40×40 to match the board.

---

## Implementation Notes

- Tokens live in **one** file: `frontend/src/shell/tokens.css`, imported by the shell before any island CSS.
- Components reference `var(--token)` exclusively. A hex color or a raw `box-shadow` stack in a component file is a bug — fix it, don't extend it.
- The tactile shadow stacks are long. **Define them once as variables and compose.** If a component needs a variant (e.g., raised + glow), stack them: `box-shadow: var(--elev-raised), 0 0 12px var(--accent-glow)`.
- Do not introduce a second accent color. The system has exactly one warm highlight; if you need to encode more state, reach for `--danger`, `--warning`, `--success`, or an icon — not a new hue.
- When in doubt, **match the reference styleguide board.** If this document disagrees with the board, the board wins and this document is out of date.
