/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/p2p"
)

func TestStopDoesNotSendOnAClosedChannel(t *testing.T) {
	odr := newOdrBackend(nil, 1)
	lp, err := NewLightProtocol("test", nil, nil, nil, false, odr, log.GetLogger("light-stop-test"), 1)
	if err != nil {
		t.Fatal(err)
	}
	// A peer that never completed the p2p handshake has no disconnection channel.
	lp.peerSet.Add(&peer{peerID: common.BytesToAddress([]byte{1})})

	lp.Stop()
	lp.requestSync()
	lp.Stop()

	odr.close()
	if err = odr.deliver(&p2p.Message{Code: blockResponseCode}); err == nil {
		t.Fatal("deliver after odr close returned nil")
	}
	odr.close()
	if err = odr.deliver(&p2p.Message{Code: blockResponseCode}); err == nil {
		t.Fatal("deliver after a second odr close returned nil")
	}
}
