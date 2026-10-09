// Table is sbx's one table idiom: every data table sbx renders — the
// status report's Creation drift, `sbx list`'s machine-wide listing —
// draws through this helper, so a reader learns one shape. The look
// follows Charm's showcase table: a thin frame in faint gray so it
// separates without competing with the cells, a bold, centered,
// uppercased header in bright yellow; body cells stay unstyled. One
// space of padding surrounds every cell; the styled writers degrade all
// of it wherever color does not survive.
//
// plain drops the border glyphs: they are non-color decoration, the class
// of output only the --plain accessibility lever removes. Cell text is
// content and always renders, so a plain table keeps every word — and
// every word's case — the bordered one shows: the header uppercase rides
// along because --no-color is byte-identical to --plain, and the header
// words themselves are the content either way.
package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// The table's colors. The header takes bright yellow — color 11, the
// Styles Heading hue, matching the section headings a table usually
// follows — and the frame recedes into bright black (dark gray on most
// terminals). Both from the named 16-color ANSI palette, like every
// Styles color; color 3 stays reserved for Warning.
const (
	tableHeader = "11" // bright yellow: header text
	tableBorder = "8"  // bright black: border glyphs
)

// Table renders headers and rows as one bordered table, each row a slice
// of cell strings as wide as the header slice. A plain table drops the
// borders and keeps the alignment.
func Table(plain bool, headers []string, rows [][]string) string {
	tab := table.New()
	tab.Headers(headers...)
	for _, r := range rows {
		tab.Row(r...)
	}
	tab.StyleFunc(func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return lipgloss.NewStyle().
				Padding(0, 1).
				Foreground(lipgloss.Color(tableHeader)).
				Bold(true).
				Align(lipgloss.Center).
				Transform(strings.ToUpper)
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})
	if plain {
		// An empty border draws no glyphs, but keeps the separators'
		// spaces: blank gutter lines and edges no reader asked for. Drop
		// the blank lines and trim each line's outer padding. The
		// inter-column gaps come from the cell padding and survive, so the
		// columns stay aligned.
		tab.Border(lipgloss.Border{})
	} else {
		tab.Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color(tableBorder)))
	}
	out := tab.String()
	if plain {
		var lines []string
		for _, line := range strings.Split(out, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				lines = append(lines, trimmed)
			}
		}
		out = strings.Join(lines, "\n")
	}
	return out
}
