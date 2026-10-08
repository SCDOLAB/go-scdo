/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package scdo

// ExternalSyncPause, when set, lets a phone pause full-block downloads for a
// low battery or a metered network. Nil means a desktop node is not gated.
var ExternalSyncPause func() (bool, string)

func syncPaused() bool {
	if ExternalSyncPause == nil {
		return false
	}
	paused, _ := ExternalSyncPause()
	return paused
}
