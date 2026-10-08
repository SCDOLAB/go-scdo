/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package heartbeat

import "time"

// Spread picks a wait in [base-jitter, base+jitter]. unit is clamped to [0, 1].
// unit 0 is the low end and unit 1 is the high end.
func Spread(base, jitter time.Duration, unit float64) time.Duration {
	if base < 0 {
		base = 0
	}
	if jitter < 0 {
		jitter = 0
	}
	if unit < 0 {
		unit = 0
	}
	if unit > 1 {
		unit = 1
	}
	delta := time.Duration(float64(2*jitter) * unit)
	out := base - jitter + delta
	if out < 0 {
		return 0
	}
	return out
}

// Backoff is the wait after failures failed POSTs. The first failure waits
// base. Each further failure doubles that wait, and the result never exceeds cap.
func Backoff(failures int, base, cap time.Duration) time.Duration {
	if failures < 1 {
		return 0
	}
	if base <= 0 {
		base = BackoffBase
	}
	if cap < base {
		cap = base
	}
	wait := base
	for i := 1; i < failures; i++ {
		if wait >= cap || wait > cap/2 {
			return cap
		}
		wait *= 2
		if wait >= cap {
			return cap
		}
	}
	return wait
}
