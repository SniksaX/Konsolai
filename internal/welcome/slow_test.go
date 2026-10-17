package welcome

import (
	"testing"
	"time"
)

func TestStatusLimitOverride(t *testing.T) {
	slow := 2 * time.Second
	if statusForLimit("main", slow-time.Millisecond, slow) != StatusUp {
		t.Fatal("main under the override should be up")
	}
	if statusForLimit("main", slow, slow) != StatusSlow {
		t.Fatal("main at the override should be slow")
	}
	if statusForLimit("embed", slow, slow) != StatusUp {
		t.Fatal("embed at the override should be up")
	}
	if statusForLimit("embed", slow+time.Millisecond, slow) != StatusSlow {
		t.Fatal("embed over the override should be slow")
	}
}
