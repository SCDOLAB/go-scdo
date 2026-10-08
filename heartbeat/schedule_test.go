/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package heartbeat

import (
	"testing"
	"time"
)

func TestSpreadStaysInsideJitter(t *testing.T) {
	base := 300 * time.Second
	jitter := 30 * time.Second
	if got := Spread(base, jitter, 0); got != base-jitter {
		t.Fatalf("low %s", got)
	}
	if got := Spread(base, jitter, 1); got != base+jitter {
		t.Fatalf("high %s", got)
	}
	if got := Spread(base, jitter, 0.5); got != base {
		t.Fatalf("mid %s", got)
	}
	if got := Spread(base, jitter, 2); got != base+jitter {
		t.Fatalf("clamp high %s", got)
	}
	if got := Spread(base, jitter, -1); got != base-jitter {
		t.Fatalf("clamp low %s", got)
	}
	if got := Spread(base, 0, 0.2); got != base {
		t.Fatalf("no jitter %s", got)
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	base := 30 * time.Second
	capWait := 30 * time.Minute
	want := []time.Duration{
		30 * time.Second,
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		16 * time.Minute,
		30 * time.Minute,
		30 * time.Minute,
	}
	for i, exp := range want {
		if got := Backoff(i+1, base, capWait); got != exp {
			t.Fatalf("failure %d: got %s want %s", i+1, got, exp)
		}
	}
	if got := Backoff(0, base, capWait); got != 0 {
		t.Fatalf("no failure waits %s", got)
	}
	if got := Backoff(3, time.Minute, 90*time.Second); got != 90*time.Second {
		t.Fatalf("cap %s", got)
	}
}
