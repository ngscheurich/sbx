package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ngscheurich/sbx/internal/ui"
)

type helpFlag struct {
	name    string
	summary string
}

type commandHelp struct {
	summary string
	usage   string
	details string
	flags   []helpFlag
}

func helpForCommand(name string) (commandHelp, bool) {
	switch name {
	case "plan":
		return commandHelp{
			summary: "Preview this worktree’s persistent sandbox",
			usage:   "sbx plan [--json]",
			details: "Read-only: inspect the intended configuration and existing sandbox, changing nothing.\nRequires a Git worktree with sbx.toml. Does not pull images, run Image checks, or reserve ports.",
			flags:   []helpFlag{{name: "--json", summary: "Write the plan as JSON"}},
		}, true
	case "build":
		return commandHelp{
			summary: "Build and import this project’s image",
			usage:   "sbx build [--keep-archive] [--json]",
			details: "Requires a [build] section in sbx.toml inside a Git worktree, Docker with buildx,\nand a local microsandbox backend. Builds with Docker, imports the image, and runs\nany declared Image check. A failed check fails the build; the imported image stays cached.",
			flags: []helpFlag{
				{name: "--keep-archive", summary: "Keep the temporary image archive for debugging, even on failure"},
				{name: "--json", summary: "Write the result as JSON; build/check progress goes to stderr"},
			},
		}, true
	case "run":
		return commandHelp{
			summary: "Run a guest command in a disposable sandbox",
			usage:   "sbx run [-- <argv...>]",
			details: "Requires a Git worktree with sbx.toml. With no guest command, opens the configured shell.\nPublishes no ports; the backend removes the sandbox and its Sandbox volumes afterward.\nWorkspace writes and Project volumes can outlive the run. Image checks gate use;\nBootstrap runs before the guest command and failure ends the run.\nGuest stdout, stderr, and exit status pass through; --json is not supported.",
		}, true
	case "up":
		return commandHelp{
			summary: "Create or start this worktree’s persistent sandbox",
			usage:   "sbx up [--allow-stale] [--retry-bootstrap] [--json]",
			details: "Requires a Git worktree with sbx.toml. Reuses an owned sandbox; never recreates it\nor adopts an unowned one. Refuses Creation drift unless explicitly allowed.\nBootstrap runs after creation; incomplete initialization fails up and is not retried automatically.",
			flags: []helpFlag{
				{name: "--allow-stale", summary: "Allow Creation drift without recreating; does not bypass Image checks"},
				{name: "--retry-bootstrap", summary: "Explicitly rerun Bootstrap, including incomplete initialization"},
				{name: "--json", summary: "Write the result as JSON; check/Bootstrap progress goes to stderr"},
			},
		}, true
	case "exec":
		return commandHelp{
			summary: "Run a guest command in this worktree’s persistent sandbox",
			usage:   "sbx exec [--allow-stale] [-- <argv...>]",
			details: "Requires a Git worktree with sbx.toml. Creates or starts the sandbox if needed and\nleaves it running. With no guest command, opens the configured shell.\nRefuses Creation drift unless explicitly allowed; never adopts an unowned sandbox.\nIncomplete Bootstrap warns but allows the command so you can repair the sandbox.\nGuest stdout, stderr, and exit status pass through; --json is not supported.",
			flags: []helpFlag{
				{name: "--allow-stale", summary: "Allow Creation drift without recreating; does not bypass Image checks"},
			},
		}, true
	case "status":
		return commandHelp{
			summary: "Inspect this worktree’s persistent sandbox",
			usage:   "sbx status [--json]",
			details: "Read-only: report identity, backend state, Creation drift, Bootstrap, and ports,\nchanging nothing. Requires a Git worktree with sbx.toml. Creation drift is reported,\nnot enforced; does not create a sandbox, run Image checks, or repair port reservations.",
			flags:   []helpFlag{{name: "--json", summary: "Write the status report as JSON"}},
		}, true
	case "list":
		return commandHelp{
			summary: "List Owned sandboxes on the local backend",
			usage:   "sbx list [--json]",
			details: "Read-only: list Owned sandboxes across projects and worktrees.\nWorks from any directory without project configuration. Requires a local backend;\nunowned sandboxes are not listed. An empty JSON result is an empty array.",
			flags:   []helpFlag{{name: "--json", summary: "Write the sandbox list as JSON"}},
		}, true
	case "logs":
		return commandHelp{
			summary: "Show this worktree’s persistent sandbox logs",
			usage:   "sbx logs",
			details: "Read-only: requires a Git worktree and an existing sandbox; never creates or starts it.\nLogs pass through unchanged; --json is not supported.",
		}, true
	case "stop":
		return commandHelp{
			summary: "Stop this worktree’s persistent sandbox",
			usage:   "sbx stop [--json]",
			details: "Requires a Git worktree and an existing owned sandbox. Keeps its state and volumes;\n“sbx up” starts it again. Creation drift does not block stopping.",
			flags:   []helpFlag{{name: "--json", summary: "Write the result as JSON"}},
		}, true
	case "rm":
		return commandHelp{
			summary: "Remove this worktree’s persistent sandbox",
			usage:   "sbx rm [--yes] [--json]",
			details: "Requires a Git worktree and an existing owned sandbox. Requires confirmation:\ninteractive use asks first; noninteractive use must pass --yes.\nRemoves Sandbox volumes and clears creation/Bootstrap records from host state.\nProject volumes and port reservations are kept. Creation drift does not block removal.\nJSON output never implies confirmation; scripts use “sbx rm --yes --json”.",
			flags: []helpFlag{
				{name: "--yes", summary: "Confirm removal without prompting (required for noninteractive use)"},
				{name: "--json", summary: "Write the result as JSON"},
			},
		}, true
	case "port prune":
		return commandHelp{
			summary: "Release reservations for sandboxes that no longer exist",
			usage:   "sbx port prune [--json]",
			details: "Requires a local backend, not a Git worktree or project configuration.\nKeeps reservations for existing sandboxes, including stopped ones.\nIf the backend cannot be inspected, fails safely, changing nothing.\nOnly port reservations are removed; sandboxes and volumes are untouched.",
			flags:   []helpFlag{{name: "--json", summary: "Write the released reservations as JSON"}},
		}, true
	case "version":
		return commandHelp{
			summary: "Show version information",
			usage:   "sbx version [--json]",
			details: "Works without project configuration or a backend.",
			flags:   []helpFlag{{name: "--json", summary: "Write version information as JSON"}},
		}, true
	default:
		return commandHelp{}, false
	}
}

func renderCommandHelp(name string, st ui.Styles) (string, bool) {
	if name == "port" {
		return renderPortHelp(st), true
	}
	h, ok := helpForCommand(name)
	if !ok {
		return "", false
	}
	flags := append(h.flags, helpFlag{name: "-h, --help", summary: "Show this help and exit"})
	width := 0
	for _, f := range flags {
		width = max(width, len(f.name))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "sbx %s — %s\n\n", name, h.summary)
	fmt.Fprintf(&b, "%s\n  %s\n\n", st.Heading.Render("Usage:"), h.usage)
	fmt.Fprintf(&b, "%s\n\n", h.details)
	fmt.Fprintf(&b, "%s\n", st.Heading.Render("Flags:"))
	for _, f := range flags {
		fmt.Fprintf(&b, "  %s%s  %s\n", st.Command.Render(f.name), strings.Repeat(" ", width-len(f.name)), f.summary)
	}
	return b.String(), true
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "-h", "--help":
			return true
		}
	}
	return false
}

func runHelp(args []string, out *output) int {
	if slices.Contains(args, "--json") {
		return out.usagef("help does not support --json")
	}
	if len(args) == 0 {
		fmt.Fprint(out.stdout, renderHelp(out.styles))
		return exitOK
	}
	name := strings.Join(args, " ")
	text, ok := renderCommandHelp(name, out.styles)
	if !ok {
		return out.usagef("unknown help command %q; run “sbx help” for usage", name)
	}
	fmt.Fprint(out.stdout, text)
	return exitOK
}
