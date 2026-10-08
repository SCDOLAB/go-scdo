/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package mobile

import (
	"crypto/ecdsa"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/heartbeat"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/node"
	"github.com/scdoproject/go-scdo/p2p"
	"github.com/scdoproject/go-scdo/scdo"
)

// rewardAddress and rewardURL are process-wide. Both must be set or the
// heartbeat stays off. The node p2p key signs; a wallet key is never loaded here.
var (
	rewardAddress string
	rewardURL     string
)

func newPhoneReward(dataDir, kind string, tips func() []heartbeat.Tip) (*heartbeat.Service, error) {
	key, err := phoneNodeKey(dataDir)
	if err != nil {
		return nil, err
	}
	return heartbeat.New(heartbeat.Config{
		Address: rewardAddress,
		URL:     rewardURL,
		Kind:    kind,
		Client:  heartbeat.ClientMobile,
		Key:     key,
		Tips:    tips,
		Log:     log.GetLogger("heartbeat"),
	})
}

func phoneNodeKey(dataDir string) (*ecdsa.PrivateKey, error) {
	cfg := &p2p.Config{}
	if err := loadP2PKey(cfg, dataDir); err != nil {
		return nil, err
	}
	return cfg.PrivateKey, nil
}

func registerReward(n *node.Node, dataDir, kind string, tips func() []heartbeat.Tip) (*heartbeat.Service, error) {
	svc, err := newPhoneReward(dataDir, kind, tips)
	if err != nil {
		return nil, err
	}
	if err = n.Register(svc); err != nil {
		return nil, err
	}
	return svc, nil
}

func (s *session) rewardTips() []heartbeat.Tip {
	if s == nil {
		return nil
	}
	s.tipMu.RLock()
	tips := make([]heartbeat.Tip, 0, len(s.full)+common.ShardCount)
	for shard, svc := range s.full {
		if tip, ok := fullTip(shard, svc); ok {
			tips = append(tips, tip)
		}
	}
	s.tipMu.RUnlock()
	if s.api != nil {
		for _, head := range s.api.Heads() {
			tips = append(tips, heartbeat.Tip{Shard: head.Shard, Height: head.Height, Hash: head.Hash})
		}
	}
	return tips
}

func (s *session) setFull(shard uint, svc *scdo.ScdoService) {
	s.tipMu.Lock()
	if s.full == nil {
		s.full = map[uint]*scdo.ScdoService{}
	}
	s.full[shard] = svc
	s.tipMu.Unlock()
}

func fullTip(shard uint, svc *scdo.ScdoService) (heartbeat.Tip, bool) {
	if svc == nil || svc.BlockChain() == nil {
		return heartbeat.Tip{}, false
	}
	block := svc.BlockChain().CurrentBlock()
	if block == nil || block.Header == nil || block.HeaderHash == (common.Hash{}) {
		return heartbeat.Tip{}, false
	}
	return heartbeat.Tip{Shard: shard, Height: block.Header.Height, Hash: block.HeaderHash, Full: true}, true
}
