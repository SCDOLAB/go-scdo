/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package scdo

import (
	"testing"
	"time"

	"github.com/scdoproject/go-scdo/core"
	"github.com/scdoproject/go-scdo/log"
	downloader "github.com/scdoproject/go-scdo/scdo/download"
)

func TestStopDoesNotSendOnAClosedChannel(t *testing.T) {
	sp := &ScdoProtocol{
		log:        log.GetLogger("protocol-stop"),
		quitCh:     make(chan struct{}),
		syncCh:     make(chan struct{}),
		downloader: downloader.NewDownloader(core.NewTestBlockchain(), nil),
	}
	sp.wg.Add(1)
	go func() {
		defer sp.wg.Done()
		<-sp.quitCh
	}()

	done := make(chan struct{})
	go func() {
		sp.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return")
	}

	sp.requestSync()
	sp.Stop()
	select {
	case <-sp.syncCh:
		t.Fatal("Stop closed syncCh")
	default:
	}
}

func TestRequestSyncAfterQuitDoesNotBlock(t *testing.T) {
	sp := &ScdoProtocol{
		log:    log.GetLogger("protocol-stop"),
		quitCh: make(chan struct{}),
		syncCh: make(chan struct{}),
	}
	close(sp.quitCh)
	done := make(chan struct{})
	go func() {
		sp.requestSync()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("requestSync blocked after quit")
	}
}
