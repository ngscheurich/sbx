// Table is sbx's one table idiom: every data table sbx renders — the
// status report's Creation drift, `sbx list`'s machine-wide listing —
// draws through this helper, so a reader learns one shape. Bordered, with
// one space of padding around every cell and bold headers; the styled
// writers strip the bold wherever color does not survive.
//
// plain drops the border glyphs: they are non-color decoration, the class
// of output only the --plain accessibility lever removes (ADR-0007).
// Cell text itself is content and always renders, so a plain table keeps
// every word the bordered one shows.
package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
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
		padded := lipgloss.NewStyle().Padding(0, 1)
		if row == table.HeaderRow {
			return padded.Bold(true)
		}
		return padded
	})
	if plain {
		// An empty border draws no glyphs, but keeps the separators'
		// spaces: blank gutter lines and edges no reader asked for. Drop
		// the blank lines and trim each line's outer padding. The
		// inter-column gaps come from the cell padding and survive, so the
		// columns stay aligned.
		tab.Border(lipgloss.Border{})
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
