package ids

import (
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func TestNew_IsVersion7AndMonotonic(t *testing.T) {
	a, b := New(), New()
	testutil.Equal(t, 7, int(a.Version()))
	testutil.True(t, a.String() < b.String(), "v7 ids are time-ordered")
}
