// Package ui is sbx's output-styling seam: it owns the writer wrapping
// that degrades styling by destination and the style set every renderer
// takes. cli.Run wraps its stdout/stderr here once; every sbx-authored
// line flows through the wrapped writers, while subprocess stdio — guest
// commands, Docker, msb — passes through the raw writers untouched, so a
// pipe never strips a guest's own escape codes.
//
// The styles themselves are profile-independent: renderers always emit
// their decoration, and the writer-bound colorprofile degrades it before
// output. A pipe or NO_COLOR therefore cannot receive ANSI no matter what
// a renderer emits, which keeps Plain output (CONTEXT.md) byte-identical
// by construction rather than by testing.
package ui

import (
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// Styles is the palette every sbx-authored surface renders through: color
// and bold only, from the named 16-color ANSI palette, with color as
// decoration on words that already carry the meaning
// (docs/style/accessibility.md). A zero Styles renders nothing; use
// NewStyles.
type Styles struct {
	// Error styles the `sbx:` prefix of error lines: ANSI color 1, bold.
	// The message text after it stays default.
	Error lipgloss.Style
	// Warning styles warnings and Project-volume conflicts: ANSI color 3.
	Warning lipgloss.Style
	// Positive styles positive states such as `creation drift: none` and
	// `reuses the existing volume`: ANSI color 2.
	Positive lipgloss.Style
	// Heading styles section headings and report title lines: bold.
	Heading lipgloss.Style
	// Command styles command names in help: bold.
	Command lipgloss.Style
}

// NewStyles returns the palette.
func NewStyles() Styles {
	return Styles{
		Error:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		Warning:  lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		Positive: lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		Heading:  lipgloss.NewStyle().Bold(true),
		Command:  lipgloss.NewStyle().Bold(true),
	}
}

// Writers wraps stdout and stderr for sbx's own lines and returns the
// wrapped writers plus the style set. Profile detection is colorprofile's,
// inherited rather than reimplemented: NO_COLOR, CLICOLOR, CLICOLOR_FORCE,
// and TERM behave as it defines, and FORCE_COLOR is not honored.
//
// One deviation, demanded by Plain output's definition: NO_COLOR at a
// terminal must yield ANSI-free bytes, while colorprofile's Ascii profile
// keeps bold as text decoration. NO_COLOR therefore forces the
// fully-stripping NoTTY profile, following no-color.org's rule that any
// non-empty value counts.
func Writers(stdout, stderr io.Writer) (io.Writer, io.Writer, Styles) {
	// os.Environ() is passed explicitly: colorprofile.NewWriter documents a
	// nil environ as "use os.Environ()" but v0.4.3 reads it as empty, and
	// an empty environment is a dumb terminal — every profile knob
	// (CLICOLOR_FORCE included) would go unread.
	env := os.Environ()
	out := colorprofile.NewWriter(stdout, env)
	errs := colorprofile.NewWriter(stderr, env)
	if os.Getenv("NO_COLOR") != "" {
		out.Profile = colorprofile.NoTTY
		errs.Profile = colorprofile.NoTTY
	}
	return out, errs, NewStyles()
}
