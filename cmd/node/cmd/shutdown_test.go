/**
* @file
* @copyright defined in scdo/LICENSE
 */

package cmd

import (
	"errors"
	"testing"
	"time"
)

func TestStopWithinReturnsTheStopError(t *testing.T) {
	want := errors.New("stopped")
	err := stopWithin(time.Second, func() error { return want })
	if err != want {
		t.Fatalf("got %v", err)
	}
}

func TestStopWithinTimesOut(t *testing.T) {
	err := stopWithin(20*time.Millisecond, func() error {
		time.Sleep(time.Second)
		return nil
	})
	if err == nil {
		t.Fatal("expected timeout")
	}
}
