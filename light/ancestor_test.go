/**
* @file
* @copyright defined in scdo/LICENSE
 */

package light

import (
	"math/big"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/core"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/log"
)

func testLightPeer(chain *core.Blockchain) *peer {
	return &peer{
		protocolManager: &LightProtocol{chain: chain},
		log:             log.GetLogger("light-ancestor-test"),
		hashIndex:       make(map[common.Hash]uint64),
	}
}

func TestFindAncestorWalksBackPastALosingTip(t *testing.T) {
	bc := core.NewTestBlockchain()
	genesis := bc.CurrentBlock()
	losingHeader := &types.BlockHeader{
		PreviousBlockHash: genesis.HeaderHash,
		Height:            genesis.Header.Height + 1,
		Difficulty:        big.NewInt(2),
		CreateTimestamp:   big.NewInt(2),
	}
	losing := &types.Block{Header: losingHeader, HeaderHash: losingHeader.Hash()}
	if err := bc.GetStore().PutBlock(losing, big.NewInt(3), true); err != nil {
		t.Fatal(err)
	}
	bc.UpdateCurrentBlock(losing)

	p := testLightPeer(bc)
	winning := common.StringToHash("winning-9274822")
	p.setHashWindow(genesis.Header.Height, []common.Hash{genesis.HeaderHash, winning})

	got, err := p.findAncestor()
	if err != nil {
		t.Fatal(err)
	}
	if got != genesis.Header.Height {
		t.Fatalf("ancestor %d, want %d", got, genesis.Header.Height)
	}
}

func TestFindAncestorAsksBelowADivergentWindow(t *testing.T) {
	bc := core.NewTestBlockchain()
	genesis := bc.CurrentBlock()
	losingHeader := &types.BlockHeader{
		PreviousBlockHash: genesis.HeaderHash,
		Height:            genesis.Header.Height + 1,
		Difficulty:        big.NewInt(2),
		CreateTimestamp:   big.NewInt(2),
	}
	losing := &types.Block{Header: losingHeader, HeaderHash: losingHeader.Hash()}
	if err := bc.GetStore().PutBlock(losing, big.NewInt(3), true); err != nil {
		t.Fatal(err)
	}
	bc.UpdateCurrentBlock(losing)

	p := testLightPeer(bc)
	p.setHashWindow(losing.Header.Height, []common.Hash{common.StringToHash("other-fork")})

	got, err := p.findAncestor()
	if err != errAncestorBelowWindow {
		t.Fatalf("err %v height %d", err, got)
	}
	if got != losing.Header.Height {
		t.Fatalf("resume height %d, want %d", got, losing.Header.Height)
	}
	if err = p.requestAncestorWindow(got); err != nil {
		t.Fatal(err)
	}
	if p.pendingAncestorBegin == 0 || len(p.blockHashArr) != 0 {
		t.Fatalf("window was not moved back: begin %d len %d", p.pendingAncestorBegin, len(p.blockHashArr))
	}

	stale := &HeaderHashSync{
		BeginNum:        genesis.Header.Height,
		HeaderArr:       []common.Hash{genesis.HeaderHash, common.StringToHash("stale")},
		TD:              big.NewInt(1),
		CurrentBlockNum: genesis.Header.Height + 1,
	}
	if err = p.handleSyncHash(stale); err != nil {
		t.Fatal(err)
	}
	if len(p.blockHashArr) != 0 {
		t.Fatal("stale hash batch replaced the rewind request")
	}
}

func TestFindAncestorSkipsHeightsThePeerHasAndWeDoNot(t *testing.T) {
	bc := core.NewTestBlockchain()
	genesis := bc.CurrentBlock()
	p := testLightPeer(bc)
	p.setHashWindow(genesis.Header.Height, []common.Hash{
		genesis.HeaderHash,
		common.StringToHash("not-local-yet"),
	})
	got, err := p.findAncestor()
	if err != nil {
		t.Fatal(err)
	}
	if got != genesis.Header.Height {
		t.Fatalf("ancestor %d, want %d", got, genesis.Header.Height)
	}
}
