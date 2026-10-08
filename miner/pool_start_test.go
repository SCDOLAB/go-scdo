/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package miner

import (
	"sync"
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

func TestPrepareNewBlockIgnoresConcurrentTaskClear(t *testing.T) {
	m := createMiner()
	m.poolMode = true

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				// SubmitWork does this when a block is found.
				m.setCurrent(nil)
			}
		}
	}()

	for i := 0; i < 8; i++ {
		if err := m.prepareNewBlock(m.recv); err != nil {
			close(stop)
			wg.Wait()
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()

	task := m.getCurrent()
	if task == nil || task.header == nil {
		// The clearer may win the race after the last publish. Run one
		// prepare with it stopped so the published task is the one we check.
		if err := m.prepareNewBlock(m.recv); err != nil {
			t.Fatal(err)
		}
		task = m.getCurrent()
	}
	if task == nil || task.header == nil {
		t.Fatal("prepared pool task was not published")
	}
	if task.generateBlock() == nil {
		t.Fatal("published pool task has no block")
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
