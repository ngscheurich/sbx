# Accessibility in sbx

How sbx's output stays usable for everyone who reads a terminal — independent of any one language. For Go code style see [`go.md`](go.md); for the prose style see [`prose.md`](prose.md).

Accessibility is a core principle, not a finishing pass. The one rule the rest of this guide follows: **no piece of meaning rides on a single perceptual channel** — color, dimness, position, or motion — that some reader can't perceive. Two audiences make that concrete, and they fail differently:

- **Low vision or color vision deficiency** — sighted, but can't rely on color (red-green deficiency alone is roughly 8% of men of Northern European descent) or on fine contrast.
- **Screen reader and braille users** — read the terminal's rendered text linearly and perceive neither color nor layout.

## Color and de-emphasis are redundant, never the message

sbx's user-facing output is plain text by construction — the `sbx plan` report on stdout and `sbx: …` error lines on stderr — and that is the baseline the rest of this guide protects. Styled output ([ADR-0007](../../docs/adrs/0007-style-output-with-charm-v2.md)) is the same content with decoration on top; the principle holds on every line.

- Pair every styled state with a word or glyph that carries the meaning on its own; color is decoration on top. If success is ever colored green and errors red, the words `error:` and the state names tell them apart on their own; color only speeds up the terminal case. The rendered plan already works this way: every state is named in words (`translation failed`, `compatibility could not be checked`) before any styling could exist.
- For a screen reader, color isn't merely hard to tell apart — it's **absent**: the terminal consumes the escape codes before the reader reaches the text. Color-only encoding fails the color-vision audience and the screen reader audience at once.
- Don't let red-versus-green be the only distinction; differ by word, symbol, or position too.
- Styling uses Lip Gloss v2, and sticks to the named 16-color ANSI palette (`lipgloss.Color("1")`–`"15"`). The user's theme defines what those look like, so they adapt to light and dark backgrounds for free. Reach for `lipgloss.AdaptiveColor` only when you hardcode specific shades, and never assume the background is light or dark.
- `Muted`/faint **reduces** contrast against a baseline you don't control, and terminals honor it inconsistently — some render it dimmer, some ignore it outright. Use it only on text that is already secondary; never to signal that something is secondary.

## Degrade by destination, and keep an explicit plain lever

Output should degrade by where it's going: styled at a terminal, plain when piped. sbx's reports are plain by construction — every renderer is one code path, and an `Ascii`-profile writer strips the escapes before output, so a pipe receives plain output no matter what the renderer emitted — and that is the property to keep. Route all human-facing output through the writers `cli.Run` receives (`stdout`/`stderr` parameters), wrapped once by `internal/ui`'s `ui.Writers`, never raw `os.Stdout`, so that gate stays in one seam rather than scattering `fmt.Println` and Lip Gloss calls across the codebase.

- Color detection is **inherited** from `colorprofile` beneath Lip Gloss v2: the profile is detected per writer from `NO_COLOR`, `CLICOLOR`/`CLICOLOR_FORCE`, and `TERM`. Don't reimplement it, and don't honor `FORCE_COLOR` — that's a Node convention `colorprofile` doesn't read; `CLICOLOR_FORCE` is the knob.
- That detection governs **ANSI styling only** — color and emphasis together. Border glyphs are content, gated separately by whether stdout is a terminal, so `NO_COLOR` at an interactive terminal would still draw borders. Since no portable signal says "a screen reader is attached," an explicit `--plain`/`--no-color` flag is the only lever that drops borders and in-place redraws at a TTY. Treat it as an accessibility lever, not just a piping convenience. It is deferred until the first non-color decoration lands, tracked in `.tracker/output-styling/issues/03-plain-lever.md`.
- Guest commands' stdio passes through to the user untouched. sbx's own lines around them must make sense read linearly — a heading, fields one per line, indented detail — with no meaning carried by cursor movement.
- The error path must read as complete and ordered in plain text: `sbx: <what failed>: <why>`, one per line, color optional. The severity prefix and the operation name carry the meaning; color is decoration.

## Screen reader and braille friendliness

- sbx has no interactive TUI. The one interactive surface is `sbx rm`'s confirmation prompt, and it must stay a plain question on its own line, answered from stdin — never a selection list redrawn in place. Noninteractive use already has `--yes`; that flag is the scripted and screen-reader surface, no interaction required.
- Interactive prompts beyond that (a picker, a form) should not be added casually. If one ever is, it needs an accessible, linear mode that is strictly opt-in — never enabled implicitly because stdin happens not to be a terminal.
- Output is read top to bottom, left to right. Borders, box-drawing, ASCII art, and space-aligned columns are announced as noise or carry no structure. Keep the meaning in words. (The plan report's aligned `key:` columns are acceptable — each line still reads as a phrase — but the meaning must never depend on the alignment.)
- Don't repaint in place, and don't repaint when plain output is requested. Spinners that use `\r` and cursor moves flood a screen reader with repeated lines; emit discrete state-change lines instead. `sbx build` is the long-running command where the temptation will arise.

## Glyphs, motion, and timing

- If a success or status glyph is ever added, pair it with a word — the meaning survives without the glyph. Provide an ASCII fallback, and don't assume the font covers it or that a screen reader pronounces it sensibly.
- Make animation possible to turn off (it folds into `--plain`): motion is distraction and vestibular load, and redraws flood screen readers. Outright strobing is unlikely in a text UI, but it's cheap to never emit.
- Don't assume fast sighted reading. Avoid timeouts on interactive prompts; if one is unavoidable, make it configurable.

## If a TUI ever lands

No TUI is planned. If a full-screen application ever arrives, remember that it is effectively opaque to a screen reader, so the CLI is the accessible surface: every TUI operation must have a CLI equivalent, everything reachable by keyboard, keybindings that are discoverable and consistent, and a layout that reflows on resize rather than truncating essential information. Keep that parity from day one of the TUI, not as a retrofit.

## Where this is enforced

The contract belongs at the output seam. That seam is `internal/ui`: `ui.Writers` wraps the `stdout`/`stderr` writers `cli.Run` receives in `colorprofile.Writer`s and hands back the detected styles. As output grows, keep human-facing text going through that seam — never raw `os.Stdout` — and keep the golden tests asserting byte-identical plain output and that severity words survive a non-terminal writer, so color-on-color-only encoding can't slip in.
