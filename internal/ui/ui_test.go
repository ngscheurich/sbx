package ui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// styledLine is one sbx-authored line carrying every decoration the
// palette uses: a bold heading, a positive state, a warning, and the
// error prefix, with plain words between them.
func styledLine(st Styles) string {
	return st.Heading.Render("sandbox:") + " " +
		st.Positive.Render("creation drift: none") + " " +
		st.Warning.Render("warning: the port registry could not be read") + " " +
		st.Error.Render("sbx:") + " plain words\n"
}

// plainLine is what a pipe must receive for styledLine.
const plainLine = "sandbox: creation drift: none warning: the port registry could not be read sbx: plain words\n"

// TestWritersStripAllDecorationFromABuffer pins the structural guarantee:
// a writer that is not a terminal — a pipe or a buffer — receives Plain
// output: every escape stripped, words untouched.
func TestWritersStripAllDecorationFromABuffer(t *testing.T) {
	var buf bytes.Buffer
	out, _, st := Writers(&buf, io.Discard)
	fmt.Fprint(out, styledLine(st))
	if got := buf.String(); got != plainLine {
		t.Errorf("buffer received %q, want the plain bytes %q", got, plainLine)
	}
	if strings.ContainsRune(buf.String(), 0x1b) {
		t.Errorf("buffer received escape bytes: %q", buf.String())
	}
}

// TestWritersKeepDecorationWhenColorIsForced checks the styled path:
// CLICOLOR_FORCE is the knob that makes a non-terminal writer carry the
// escapes, and every word survives alongside them.
func TestWritersKeepDecorationWhenColorIsForced(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	var buf bytes.Buffer
	out, _, st := Writers(&buf, io.Discard)
	fmt.Fprint(out, styledLine(st))
	got := buf.String()
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("forced-color buffer carries no ANSI escapes: %q", got)
	}
	for _, want := range []string{"sandbox:", "creation drift: none", "warning:", "sbx:", "plain words"} {
		if !strings.Contains(got, want) {
			t.Errorf("styled output lost the word %q: %q", want, got)
		}
	}
}

// TestWritersStripAllDecorationUnderNoColorAtATerminal pins Plain
// output's definition at a terminal: NO_COLOR must yield the same ANSI-free
// bytes a pipe receives. colorprofile's Ascii profile keeps bold as text
// decoration, so this test is what forces the fully-stripping profile.
func TestWritersStripAllDecorationUnderNoColorAtATerminal(t *testing.T) {
	t.Setenv("TTY_FORCE", "1") // colorprofile's knob: treat the writer as a terminal
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	out, _, st := Writers(&buf, io.Discard)
	fmt.Fprint(out, styledLine(st))
	if got := buf.String(); got != plainLine {
		t.Errorf("NO_COLOR at a terminal produced %q, want the plain bytes %q", got, plainLine)
	}
}

// TestWritersTreatEmptyNoColorAsUnset follows no-color.org: NO_COLOR counts
// as set only when present and non-empty, so an empty value leaves the
// forced color profile alone.
func TestWritersTreatEmptyNoColorAsUnset(t *testing.T) {
	t.Setenv("TTY_FORCE", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	var buf bytes.Buffer
	out, _, st := Writers(&buf, io.Discard)
	fmt.Fprint(out, styledLine(st))
	if !strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("empty NO_COLOR stripped the forced color: %q", buf.String())
	}
}

// TestNewStylesRendersThePalette pins the palette itself: the error prefix
// is bold and ANSI color 1, warnings color 3, positive states color 2,
// headings and command names bold — and nothing else. Each styled string
// must strip back to exactly its text.
func TestNewStylesRendersThePalette(t *testing.T) {
	st := NewStyles()
	for _, tc := range []struct {
		name  string
		style lipgloss.Style
		text  string
		want  []string // escape fragments the spec's palette demands
	}{
		{"error prefix", st.Error, "sbx:", []string{"\x1b[1", "31"}},
		{"warning", st.Warning, "warning:", []string{"33"}},
		{"positive", st.Positive, "creation drift: none", []string{"32"}},
		{"heading", st.Heading, "Usage:", []string{"\x1b[1"}},
		{"command", st.Command, "plan", []string{"\x1b[1"}},
	} {
		got := tc.style.Render(tc.text)
		if strip := ansi.Strip(got); strip != tc.text {
			t.Errorf("%s: styled %q strips to %q", tc.name, got, strip)
		}
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: styled %q lacks the palette escape %q", tc.name, got, want)
			}
		}
	}
}
