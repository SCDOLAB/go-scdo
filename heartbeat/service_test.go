/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package heartbeat

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/hexutil"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/rpc"
)

func TestRewardAPIIsExported(t *testing.T) {
	if err := rpc.NewServer().RegisterName("scdo", &API{svc: &Service{}}); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledServiceDoesNotDial(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		t.Errorf("disabled client dialed %s", r.URL)
	}))
	defer server.Close()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{
		Address:  "0x1111111111111111111111111111111111111111",
		URL:      "",
		Kind:     KindFull,
		Key:      key,
		Interval: 5 * time.Millisecond,
		Tips: func() []Tip {
			return []Tip{{Shard: 1, Height: 3, Hash: common.BytesToHash([]byte{9}), Full: true}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()
	time.Sleep(40 * time.Millisecond)
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("hits %d", hits)
	}
	status := svc.Status()
	if status.Enabled {
		t.Fatal("status enabled")
	}
	if _, err = New(Config{Address: "0x1234", URL: server.URL}); err == nil {
		t.Fatal("bad address was accepted")
	}
}

func TestHeartbeatPostsSignedSample(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := NodeID(key)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type %s", r.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var msg Message
		if err = json.Unmarshal(body, &msg); err != nil {
			t.Error(err)
			return
		}
		sig, err := hexutil.HexToBytes(msg.Sig)
		if err != nil {
			t.Error(err)
			return
		}
		msg.Sig = ""
		canonical, err := CanonicalJSON(msg)
		if err != nil {
			t.Error(err)
			return
		}
		recovered, err := crypto.Ecrecover(crypto.Keccak256(canonical), sig)
		if err != nil {
			t.Error(err)
			return
		}
		if hexutil.BytesToHex(recovered) != nodeID || msg.NodeID != nodeID {
			t.Errorf("node id recovered %s body %s", hexutil.BytesToHex(recovered), msg.NodeID)
		}
		if msg.Kind != KindFull || msg.Client != ClientGoSCDO || msg.V != Version {
			t.Errorf("message %+v", msg)
		}
		if len(msg.Shards) != 2 || msg.Shards[0].Shard != 1 || msg.Shards[1].Shard != 4 {
			t.Errorf("shards %+v", msg.Shards)
		}
		select {
		case got <- body:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"eligible_today":true,"uptime_today":0.42,"weight":3,"est_share_scdo":"server-share","pool_today_scdo":"server-pool","reason":null}`))
	}))
	defer server.Close()

	svc, err := New(Config{
		Address:  "0x3333333333333333333333333333333333333333",
		URL:      server.URL,
		Kind:     KindFull,
		Client:   ClientGoSCDO,
		Key:      key,
		Interval: 20 * time.Millisecond,
		Tips: func() []Tip {
			return []Tip{
				{Shard: 4, Height: 11, Hash: common.BytesToHash([]byte{4})},
				{Shard: 1, Height: 22, Hash: common.BytesToHash([]byte{1}), Full: true},
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Status().Enabled != true {
		t.Fatal("not enabled")
	}
	if err = svc.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no heartbeat")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := svc.Status()
		if status.OK && status.EligibleToday && status.EstShareSCDO == "server-share" && status.UptimeToday == 0.42 && status.PoolTodaySCDO == "server-pool" && status.Error == "" {
			if status.PayoutAddress != "0x3333333333333333333333333333333333333333" {
				t.Fatalf("payout %s", status.PayoutAddress)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status %+v", status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
