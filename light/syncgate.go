/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"sync"

	"github.com/scdoproject/go-scdo/log"
)

// SyncGate is process-wide. The phone app reports battery and network state;
// Go cannot read Android BatteryManager itself. The default policy is off so
// a desktop `node start -l` and the unit tests keep syncing.
type syncGate struct {
	mu                sync.Mutex
	manual            string
	pauseOnMetered    bool
	pauseOnLowBattery bool
	metered           bool
	lowBattery        bool
	protocols         []*LightProtocol
}

var gate syncGate

func (g *syncGate) pausedLocked() (bool, string) {
	if g.manual != "" {
		return true, g.manual
	}
	if g.pauseOnLowBattery && g.lowBattery {
		return true, "low-battery"
	}
	if g.pauseOnMetered && g.metered {
		return true, "metered"
	}
	return false, ""
}

func (g *syncGate) apply(mutator func()) {
	g.mu.Lock()
	was, _ := g.pausedLocked()
	mutator()
	now, reason := g.pausedLocked()
	protocols := append([]*LightProtocol(nil), g.protocols...)
	g.mu.Unlock()
	if was == now {
		return
	}
	log.GetLogger("lightsync").Info("header sync paused=%v reason=%s", now, reason)
	if now {
		for _, lp := range protocols {
			lp.cancelDownload()
		}
		return
	}
	for _, lp := range protocols {
		lp.requestSync()
	}
}

// SyncPause reports whether header sync should stay idle. The stored tip is kept.
func SyncPause() (bool, string) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.pausedLocked()
}

// Pause stops header downloads until Resume. reason is reported to the wallet.
func Pause(reason string) {
	if reason == "" {
		reason = "paused"
	}
	gate.apply(func() { gate.manual = reason })
}

// Resume clears a manual pause. A metered or low-battery policy can still hold sync.
func Resume() {
	gate.apply(func() { gate.manual = "" })
}

// SetSyncPolicy chooses which device states pause header sync.
// Both flags default to false. The mobile package pauses on a low battery
// and leaves a metered network (5G) running.
func SetSyncPolicy(pauseOnMetered, pauseOnLowBattery bool) {
	gate.apply(func() {
		gate.pauseOnMetered = pauseOnMetered
		gate.pauseOnLowBattery = pauseOnLowBattery
	})
}

// DeviceMetered reports the last network state from SetDeviceState.
func DeviceMetered() bool {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.metered
}

// SetDeviceState is how the Android app reports a metered network or a low battery.
func SetDeviceState(metered, lowBattery bool) {
	gate.apply(func() {
		gate.metered = metered
		gate.lowBattery = lowBattery
	})
}

func registerClientProtocol(lp *LightProtocol) {
	if lp == nil || lp.bServerMode {
		return
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	for _, existing := range gate.protocols {
		if existing == lp {
			return
		}
	}
	gate.protocols = append(gate.protocols, lp)
}

func unregisterClientProtocol(lp *LightProtocol) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	out := gate.protocols[:0]
	for _, existing := range gate.protocols {
		if existing != lp {
			out = append(out, existing)
		}
	}
	gate.protocols = out
}

func (lp *LightProtocol) cancelDownload() {
	if lp == nil || lp.downloader == nil {
		return
	}
	lp.downloader.cancel()
}

// shouldRetrySession is the flaky-5G rule: a session that started and ended
// while this shard is still behind is retried. A pause does not arm the retry.
func shouldRetrySession(started bool, local, peer uint64, paused bool) bool {
	return started && !paused && peer > local
}
