/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package miner

import (
	"testing"

	"github.com/scdoproject/go-scdo/common"
)

func TestPoolMinerStartWithNilEngineReturnsError(t *testing.T) {
	coinbase := common.BytesToAddress([]byte{1})
	miner := NewMiner(common.EmptyAddress, []common.Address{coinbase}, nil, nil, nil, true)
	if err := miner.Start(); err == nil {
		t.Fatal("miner_start in pool mode with nothing initialised returned nil")
	}
}

func TestGenerateBlockNilHeaderReturnsNil(t *testing.T) {
	if block := (&Task{}).generateBlock(); block != nil {
		t.Fatal("nil header produced a block")
	}
	var task *Task
	if block := task.generateBlock(); block != nil {
		t.Fatal("nil task produced a block")
	}
}
