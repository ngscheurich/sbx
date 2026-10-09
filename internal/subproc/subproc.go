// Package subproc holds the one helper sbx's three subprocess adapters —
// msb, docker, and git — share: reading the first line of a subprocess
// output stream.
package subproc

import "strings"

// FirstLine returns the first non-empty line of s, trimmed, or the
// fallback when the stream carries nothing readable. Error detail travels
// as its first line because that is all a wrapped failure message can
// hold without burying the cause.
func FirstLine(s, fallback string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return fallback
}
