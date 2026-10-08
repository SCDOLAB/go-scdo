/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package cmd

import (
	"fmt"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/heartbeat"
	"github.com/scdoproject/go-scdo/light"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/node"
	"github.com/scdoproject/go-scdo/scdo"
	"github.com/scdoproject/go-scdo/scdo/lightclients"
	"github.com/spf13/cobra"
)

var (
	rewardAddressFlag string
	rewardURLFlag     string
)

func resolveReward(cmd *cobra.Command, cfg *node.Config) (string, string, error) {
	address := cfg.BasicConfig.RewardAddress
	endpoint := cfg.BasicConfig.RewardHeartbeatURL
	if cmd != nil && cmd.Flags().Changed("reward-address") {
		address = rewardAddressFlag
	}
	if cmd != nil && cmd.Flags().Changed("reward-heartbeat-url") {
		endpoint = rewardURLFlag
	}
	address, err := heartbeat.NormalizeAddress(address)
	if err != nil {
		return "", "", err
	}
	endpoint, err = heartbeat.NormalizeURL(endpoint)
	if err != nil {
		return "", "", err
	}
	if heartbeat.Enabled(address, endpoint) && cfg.P2PConfig.PrivateKey == nil {
		return "", "", fmt.Errorf("reward heartbeat needs the node p2p key")
	}
	return address, endpoint, nil
}

func newRewardService(cfg *node.Config, address, endpoint, kind string, tips func() []heartbeat.Tip) (*heartbeat.Service, error) {
	return heartbeat.New(heartbeat.Config{
		Address: address,
		URL:     endpoint,
		Kind:    kind,
		Client:  heartbeat.ClientGoSCDO,
		Key:     cfg.P2PConfig.PrivateKey,
		Tips:    tips,
		Log:     log.GetLogger("heartbeat"),
	})
}

func lightRewardTips(clients []*light.ServiceClient) []heartbeat.Tip {
	tips := make([]heartbeat.Tip, 0, len(clients))
	for _, client := range clients {
		if client == nil {
			continue
		}
		height, hash, ok := client.Head()
		if !ok {
			continue
		}
		tips = append(tips, heartbeat.Tip{Shard: client.Shard(), Height: height, Hash: hash})
	}
	return tips
}

func fullRewardTips(shard uint, svc *scdo.ScdoService, manager *lightclients.LightClientsManager) []heartbeat.Tip {
	tips := make([]heartbeat.Tip, 0, common.ShardCount)
	if svc != nil && svc.BlockChain() != nil {
		block := svc.BlockChain().CurrentBlock()
		if block != nil && block.Header != nil && block.HeaderHash != (common.Hash{}) {
			tips = append(tips, heartbeat.Tip{
				Shard:  shard,
				Height: block.Header.Height,
				Hash:   block.HeaderHash,
				Full:   true,
			})
		}
	}
	if manager == nil {
		return tips
	}
	for _, client := range manager.Clients() {
		if client == nil {
			continue
		}
		height, hash, ok := client.Head()
		if !ok {
			continue
		}
		tips = append(tips, heartbeat.Tip{Shard: client.Shard(), Height: height, Hash: hash})
	}
	return tips
}

func announceReward(address, endpoint string) {
	if !heartbeat.Enabled(address, endpoint) {
		return
	}
	fmt.Printf("Reward heartbeat on. Payout %s.\n", address)
}
