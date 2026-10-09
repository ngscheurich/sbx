// `sbx list`: the one backend-global command. Every other sbx command
// starts from the worktree — discover the root, load that checkout's
// sbx.toml — but a machine-wide view of the sandboxes sbx holds cannot,
// so list talks to the backend alone and never reads project configuration.
package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/ui"
)

// listRow is one rendered line of the listing.
type listRow struct {
	name, image, status, created string
}

// runList implements `sbx list`: every Owned sandbox the backend holds,
// across all projects and worktrees, with no project configuration read.
// The backend's listing carries no labels, so ownership is decided per
// entry through Inspect. Both reads fail closed: a failed listing or
// inspection is an error, never an empty or partial table.
func runList(ctx context.Context, out *output) int {
	entries, err := msb.CLI{}.List(ctx)
	if err != nil {
		return out.fail(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	var rows []listRow
	for _, e := range entries {
		s, err := msb.CLI{}.Inspect(ctx, e.Name)
		if err != nil {
			return out.fail(fmt.Errorf("inspecting %s: %w", e.Name, err))
		}
		if !isOwned(s) {
			continue
		}
		rows = append(rows, listRow{name: e.Name, image: e.Image, status: e.Status, created: formatCreated(e.CreatedAt)})
	}
	if len(rows) == 0 {
		fmt.Fprintln(out.stdout, "no sbx sandboxes")
		return exitOK
	}
	renderList(out, rows)
	return exitOK
}

// formatCreated renders the backend's timestamp as a fixed-format local
// date. An unparseable value is shown as the backend sent it: the column
// is display, and a wrong clock format is never worth failing the
// listing over.
func formatCreated(raw string) string {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return t.Local().Format("2006-01-02 15:04")
}

// renderList writes the table through sbx's one table idiom (ui.Table):
// the same bordered shape the status report's drift table draws, with the
// borders dropped under --plain.
func renderList(out *output, rows []listRow) {
	headers := []string{"name", "image", "status", "created"}
	tableRows := make([][]string, len(rows))
	for i, r := range rows {
		tableRows[i] = []string{r.name, r.image, r.status, r.created}
	}
	fmt.Fprintln(out.stdout, strings.TrimRight(ui.Table(out.plain, headers, tableRows), "\n"))
}
