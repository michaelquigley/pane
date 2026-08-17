# the metawoo toolbar

spec for the `add-the-metawoo-toolbar` roadmap card. work order: `metawoo-toolbar-work-order.md`.

pane is the newest surface in the family, and the family already has a standing chrome language. flo's studio and reef's observatory both open with the same piece of furniture: a quiet 2.6rem bar, a centered cluster of glyphs grouped by whitespace, a signal column at the right. the word travels in a tooltip; the current thing lights in the product's accent; everything else sits in muted ink. reef's source calls it "the one piece of standing chrome."

pane's header is the odd one out: a 64px strip of bordered text buttons, a hamburger, and a toggle that unfolds a panel under itself. this arc replaces it with the family's bar — the same geometry, the same glyph grammar, painted in pane's own palette with pane's own controls.

## the shape of the bar

- a full-width bar at the top of the window, spanning the conversation rail and the chat column. the rail and the tool panel drop below it.
- 2.6rem tall at rest — `--toolbar-height` is a floor, not a promise: on narrow windows the wrapped cluster can make the bar taller. pane's paper background, a hairline border below. the cluster centers on the window, not on the chat column — the family's look; the offset with the rail open is optical only.
- the cluster sits in a three-column grid (`minmax(0, 1fr) auto minmax(max-content, 1fr)`) capped at `min(1150px, 100%)`: empty left, cluster in the middle, signals at the right. on narrow windows the cluster wraps rather than colliding with the signals. the surfaces below the bar — the rail and the tool panel — anchor to its rendered height, so a wrapped, taller bar never obscures them.
- groups are set off by whitespace only — a 0.5rem gap span between clusters, no drawn rule.
- controls read as 18px glyphs; the word lives in the title and aria-label. links sit in muted ink, hover to full ink, and the active surface lights in pane's accent.

## what lives on the bar

| position | control | family grammar | pane behavior (unchanged) |
|---|---|---|---|
| group 1 — the conversation | new conversation | glyph action (`add_box`, flo's "new work" mark) | creates a new conversation |
| | conversations | glyph toggle (`forum`), lit while the rail is open | opens and closes the conversation rail (replaces the hamburger) |
| | export | glyph action (`upload`), disabled without an exportable conversation | downloads the active conversation as markdown |
| group 2 — the reply | model | glyph (`psychology`) beside the compact model select | the existing model dropdown, persisted in localStorage |
| | system prompt | glyph (`description`), lit on non-default modes; opens a modal holding the mode select and the custom text | the existing default / custom / none modes, persisted in localStorage |
| group 3 — machinery | tools | glyph (`construction`) wearing a count badge (shown while the count is above zero), lit while the panel is open | opens and closes the tool panel |
| signals (right) | context meter | mono readout in the signal column | unchanged: `?` for the unknown state, cool / warm / hot bands against the configured window |

grouping follows the family rhythm. reef sets off front door, estate, and machinery; here it is the conversation, the shape of the reply, and the machinery behind it.

## what goes away

- the 64px `.header` strip and its bordered text buttons
- the hamburger; the conversations glyph takes its job
- the system-prompt toggle that expanded a panel under the header
- the `Tools (n)` text button; the count moves to the glyph badge and the glyph's dynamic label

what stays: the rail's contents, the chat view, the input area, the tool panel's contents (only its top edge moves), the meter's semantics, and pane's palette — light and dark both. the bar borrows the family's geometry, not its tokens: `--page-bg` never enters this stylesheet; the bar is painted with pane's own variables.

## glyphs

sourced at grade-500 from Material Symbols (Apache-2.0) following the family's `icons.tsx` pattern: a `MaterialIcon` wrapper with `fill: currentColor`, the word never in the icon. pane's set:

| control | first choice | alternates |
|---|---|---|
| new conversation | `add_box` (shared with flo's "new work") | `note_add` |
| conversations | `forum` | `history` |
| export | `upload` | the markdown logo mark |
| model | `psychology` | `chip` |
| system prompt | `description` (already in the family set) | `text_notes` |
| tools | `construction` | `build` |

the final pick is a hands-on judgment: the glyphs are the face of the bar, and michael will want to live with them for a day. the alternates above are the fallback, not an improvisation.

## states and signals

- **lit** — a togglable surface (conversations, tools) lights in the accent while its panel is open. the family splits on how a current glyph lights: reef to the accent, flo to full ink. pane takes the accent — it is the amber's stated role (active and selected states). actions (new, export) have no lit state and only gain full ink on hover.
- **disabled** — export sits in muted ink with no hover while the active conversation has no exportable messages.
- **badge** — the tools glyph wears a small mono bubble with the discovered-tool count while the count is above zero; a zero or absent count shows no bubble. the count also rides the glyph's title and aria-label — `tools (n)` while positive, plain `tools` at zero — and the bubble itself is aria-hidden, so the number reaches assistive technology exactly once, the way the old `Tools (n)` text button exposed it. the family also tints a badge warm when failures are present; pane's server-error tint rides that pattern and is on the deferred menu below.
- **the meter** — pane's existing readout, moved to the signal column: mono, band colored, `?` for the unknown state. nothing about what it says changes.

## accessibility

the family rules, unchanged: every glyph control carries an aria-label and a title — the tools glyph's label is dynamic, carrying its count while positive; the two toggles expose `aria-pressed`; the modal carries `role="dialog"` and `aria-modal="true"` with an accessible name, a card body that scrolls within the viewport, and focus that is trapped while open and returns to its glyph on close (escape or outside click); focus-visible styling on every control; tab order follows visual order; decorative glyphs are aria-hidden.

## scenarios

michael opens pane in the morning. the window is the chat column and, if he left it open, the rail — and above both, one quiet strip: three conversation glyphs, the model select and the prompt glyph, the tools glyph wearing its count, and a small percentage at the far right. nothing is bordered, nothing is labeled in words, and everything he needs is within an arm's length of his eyes.

a long session with two MCP servers and a thinking model. the meter drifts from cool to warm as the window fills, the tools glyph keeps its badge, a pending approval glows in the message flow where the reader is already looking. the bar itself never moves — no reflow, no new row, no animation. it is standing chrome: the one piece of furniture the window never has to rebuild.

## seam census

- **control / state** — fused. the toolbar renders app state (rail and panel openness, preferences, counts, the usage record) and calls app handlers; the only new local state is the system-prompt modal's open/closed, which is presentation. *revisit when the toolbar starts owning a timer* — a connection ping would; the hook belongs in `hooks/`, not the component.
- **substrate scope** — the bar borrows the family's grammar (bar shape, grid, whitespace gaps, glyph links, signal column, badge) and paints it with pane's existing tokens. no family token names enter the stylesheet. the limit is against *unrelated* reskins, not a ban on every file this arc touches: chrome the arc leaves alone — the chat view, message and tool-call rendering, the input area, the rail's contents — takes no new rule, while the toolbar-mounted controls, the prompt modal, and the container / tool-panel geometry are the arc itself. a pane-wide reskin to the family paper is a different, larger arc. *revisit if the bar stops reading as family, or if pane enters the metawoo product line and the identity pack's wordmark-and-accent question comes due.*
- **overflow home** — the system prompt's surface (the mode select and the custom text) lives in a modal opened by its glyph (michael's call over a popover, 2026-08-17), not in a second toolbar row. flo's subbar belongs to route-scoped list filters, and pane has no routes. the mode changes only inside the modal, so there is no auto-open: the glyph is the only door, lit on non-default modes, and its lit state is the resting signal. *revisit when a second modal surface lands and a shared modal component earns its cost.*
- **dark mode** — kept. the family is light-only, but pane's dark theme predates this arc and the grammar is palette-agnostic; the bar resolves through pane's existing variables and inherits the dark block for free. *revisit if pane adopts the metawoo substrate's light-only paper.*

## deferred (and why)

a menu, not a spec — each item has a home on or beside the bar's signal column, and none changes the shape of the work. michael opts in or out per item.

- **connection indicator** — flo's ping pattern (poll every 5s, 2s while down, a warn dot and "server unreachable" in the signal column). the column is shaped to take it; pane's local-single-user posture makes it lower value until the bar hosts a reason to notice.
- **badge warm-tint** — tint the tools badge while any server reports error state. a real signal, deferred so the badge's meaning stays one number.
- **stream and approval signals** — a streaming pulse or a pending-approval badge on the bar. the message flow already shows both where the reader is looking; a duplicate channel on the chrome is noise until the bar earns it.
- **screen-reader announcer** — flo's JobAnnouncer analog for turn and approval transitions. the chat is the live region; a second one in the toolbar is an accessibility decision, not a chrome decision.
- **pane as a metawoo product** — an accent, a wordmark, and a favicon per the identity pack. a branding call, not a chrome call; the bar carries pane's own accent today.
- **settings surface** — the toolbar exposes every control pane has. where settings gather when pane grows them is a product question.

## references

- the family ground truth: `../archive/reef/server/ui/src/components/Toolbar.tsx` and `../archive/flo/studio/ui/src/components/Toolbar.tsx` with their `style.css` toolbar sections — the geometry this spec names is their working implementation.
- grimoire: `design/web/archive-family-ui-design-language` (the family language) and `design/web/metawoo-software-products-identity-pack` (the branding pack, for the deferred product-line question).
- pane's built behavior: `docs/current/pane.md` — the UI bullets this arc rewrites.