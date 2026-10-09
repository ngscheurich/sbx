package build

import (
	"testing"

	"github.com/ngscheurich/sbx/internal/harness"
)

// TestMain delegates to the shared command-test harness, which pins the
// process's local zone to UTC and cleans up the seed repository template.
func TestMain(m *testing.M) { harness.Main(m) }
