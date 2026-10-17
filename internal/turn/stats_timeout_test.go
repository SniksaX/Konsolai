package turn

import (
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/config"
)

func TestStatsInsertTimeoutFollowsProfile(t *testing.T) {
	var r Runner
	if r.statsInsertTimeout() != config.Builtin().StatsInsertTimeout() {
		t.Fatalf("zero profile %s", r.statsInsertTimeout())
	}
	r.Profile.Stats.InsertTimeoutMS = 50
	if r.statsInsertTimeout() != 50*time.Millisecond {
		t.Fatalf("override %s", r.statsInsertTimeout())
	}
}
