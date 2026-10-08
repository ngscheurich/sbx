// Golden files pin rendered output byte-for-byte: the report a pipe
// receives must not drift by a single byte.

package testsupport

import (
	"bytes"
	"flag"
	"os"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files from the produced output")

// Golden compares got with the golden file at path, relative to the
// calling package's directory, failing with both byte sequences side by
// side. With -update it rewrites the file instead of comparing.
func Golden(t *testing.T, path string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden %s: %v", path, err)
	}
	if *updateGolden {
		if !bytes.Equal(want, got) {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output drifted from the %s golden:\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
