/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package p2p

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/core"
)

func TestHandshakeEOFExplainsMissingLightServer(t *testing.T) {
	caps := []Cap{{Name: "lightScdo_1", Version: 1}}
	err := explainHandshakeRead(caps, "82.223.19.88:8057", io.EOF)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"EOF", "82.223.19.88:8057", "lightScdo_1/1", "no light server", "scdo/1"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("handshake EOF error %q missing %q", msg, want)
		}
	}
	refused := errors.New("connection refused")
	if got := explainHandshakeRead(caps, "peer", refused); got != refused {
		t.Fatalf("non-EOF error was rewritten: %v", got)
	}
}

func TestProtocolMismatchNamesLightServer(t *testing.T) {
	srv := NewServer(core.GenesisInfo{}, Config{NetworkID: "net1", PrivateKey: generatePrivKey()}, []Protocol{{
		Name:    common.ScdoProtoName,
		Version: common.ScdoVersion,
	}})
	remote := &ProtoHandShake{
		Caps:      []Cap{{Name: "lightScdo_1", Version: 1}},
		Params:    srv.genesisHash.Bytes(),
		NetworkID: "net1",
	}
	if _, err := srv.peerIsValidate(remote); err == nil || !strings.Contains(err.Error(), "protocol mismatch") || !strings.Contains(err.Error(), "lightScdo_1/1") {
		t.Fatalf("got %v", err)
	}

	serving := NewServer(core.GenesisInfo{}, Config{NetworkID: "net1", PrivateKey: generatePrivKey()}, []Protocol{
		{Name: common.ScdoProtoName, Version: common.ScdoVersion},
		{Name: "lightScdo_1", Version: 1},
	})
	remote.Params = serving.genesisHash.Bytes()
	got, err := serving.peerIsValidate(remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].String() != "lightScdo_1/1" {
		t.Fatalf("shared light cap: %+v", got)
	}
}
