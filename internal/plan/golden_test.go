package plan

// The plain-output golden: the rendered report a pipe or NO_COLOR receives
// is byte-identical to sbx's pre-styling output. The writer seam strips
// the decoration, so the golden can only ever hold plain bytes; -update
// rewrites it from the produced output.

import (
	"testing"

	"github.com/ngscheurich/sbx/internal/testsupport"
)

func TestRenderPlainGolden(t *testing.T) {
	testsupport.Golden(t, "testdata/plan.golden", []byte(renderPlain(t, testPlan(t))))
}
