# Color the bolded headings

Status: done

## Goal

The plan report's bolded entries — the header line and the section headings — are bold-only today, which reads as a wall of undifferentiated emphasis. Give the `ui.Styles.Heading` style a foreground color, ANSI color `11` (bright yellow), so the bolded entries scan at a glance. Color `3` is the palette's other yellow but already belongs to Warning, so bright `11` keeps headings and warnings distinct; like the rest of the palette, it adapts to the user's theme.

Because there is one rendering path and one shared style set, `status`'s headings and help's `Usage:`/`Commands:` lines take the same color — that spillover is the spec's one-rendering-path principle working as intended, not scope creep.

The spec's palette decision is amended to match.

## Acceptance

- `ui.NewStyles`' `Heading` renders bold with ANSI color `11`; the palette test asserts both escapes.
- Plain output stays byte-identical: the Ascii-profile writer already strips every escape, so no golden changes.
- Words still carry the meaning; color remains decoration, per `accessibility.md`.
- `go test ./...` passes.

## Comments

Done in the heading-color commit. `Heading` is now bold + ANSI color `11` (bright yellow); the palette test asserts both escapes, and every plain golden passes untouched — the Ascii-profile writer strips the new color exactly as it stripped the bold, so Plain output is unchanged by construction. The spillover is as predicted: `status`'s headings and help's `Usage:`/`Commands:` lines take the color through the shared style set. The drift heading inside `existing sandbox:` also renders cyan-bold now; it stays a heading (the plan reports drift without blocking), and its words already carry the warning.
