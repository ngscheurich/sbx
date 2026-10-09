// White-box tests for the persistent command layer's internals.
package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestConfirmRemovalAnswers pins the interactive removal prompt's accepted
// answers and the prompt text itself.
func TestConfirmRemovalAnswers(t *testing.T) {
	cases := []struct {
		answer string
		want   bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"YES\n", true},
		{" y \n", true},
		{"n\n", false},
		{"no\n", false},
		{"", false},
		{"\n", false},
	}
	for _, tc := range cases {
		var stderr bytes.Buffer
		got := confirmRemoval("app-wt1-1234abcd", strings.NewReader(tc.answer), &stderr)
		if got != tc.want {
			t.Errorf("confirmRemoval(%q) = %v, want %v", tc.answer, got, tc.want)
		}
		if !strings.Contains(stderr.String(), "Remove the persistent sandbox") {
			t.Errorf("the prompt is missing from stderr:\n%s", stderr.String())
		}
	}
}
