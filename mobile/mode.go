/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package mobile

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/light"
	"github.com/scdoproject/go-scdo/node"
	"github.com/scdoproject/go-scdo/p2p"
	"github.com/scdoproject/go-scdo/scdo"
)

const (
	// ModeLite is the header-only client.
	ModeLite = "lite"

	// ModePro is a full block and state sync from fork genesis.
	ModePro = "pro"
)

// DefaultSyncPolicy is the phone default: keep syncing on 5G, pause when
// the app reports a low battery.
func DefaultSyncPolicy() (pauseOnMetered, pauseOnLowBattery bool) {
	return false, true
}

// ParseMode accepts lite/light and pro/full. An empty mode is lite.
func ParseMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "lite", "light":
		return ModeLite, nil
	case "pro", "full":
		return ModePro, nil
	default:
		return "", fmt.Errorf("mode must be lite or pro, got %q", mode)
	}
}

// ParseShards parses "1,2,4". Empty, "all" and "*" mean shards 1-4.
func ParseShards(shards string) ([]uint, error) {
	shards = strings.TrimSpace(shards)
	if shards == "" || strings.EqualFold(shards, "all") || shards == "*" {
		out := make([]uint, 0, common.ShardCount)
		for shard := uint(1); shard <= uint(common.ShardCount); shard++ {
			out = append(out, shard)
		}
		return out, nil
	}
	parts := strings.FieldsFunc(shards, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	out := make([]uint, 0, len(parts))
	seen := make(map[uint]bool, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > common.ShardCount {
			return nil, fmt.Errorf("shard must be 1-%d, got %q", common.ShardCount, part)
		}
		shard := uint(n)
		if seen[shard] {
			continue
		}
		seen[shard] = true
		out = append(out, shard)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no shards selected")
	}
	return out, nil
}

// LiteShardDir is the verified header database for one shard.
// Pro mode never deletes this directory.
func LiteShardDir(dataDir string, shard uint) string {
	return filepath.Join(dataDir, "db", fmt.Sprintf("lightchainforshard_%d", shard))
}

// ProShardDir is the full block and state database for one shard.
func ProShardDir(dataDir string, shard uint) string {
	return filepath.Join(dataDir, "pro", fmt.Sprintf("shard%d", shard))
}

// PrepareProDirs creates the full-node directories and leaves the lite
// header directories where they are.
func PrepareProDirs(dataDir string, shards []uint) error {
	for _, shard := range shards {
		if err := os.MkdirAll(ProShardDir(dataDir, shard), 0700); err != nil {
			return err
		}
	}
	return nil
}

// DirBytes is the size of every file under root. A missing directory is 0.
func DirBytes(root string) uint64 {
	var total uint64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += uint64(info.Size())
		}
		return nil
	})
	return total
}

func proListenAddr(shard uint) string {
	return fmt.Sprintf("0.0.0.0:%d", 18057+int(shard)-1)
}

func proRPCAddr(shard, primary uint) string {
	if shard == primary {
		return RPCAddress
	}
	return fmt.Sprintf("127.0.0.1:%d", 18037+int(shard))
}

func baseConfig(dataDir string) *node.Config {
	conf := &node.Config{}
	conf.BasicConfig.Name = "SCDO Mobile"
	conf.BasicConfig.Version = common.ScdoNodeVersion
	conf.BasicConfig.DataDir = dataDir
	conf.BasicConfig.MinerAlgorithm = common.ZpowAlgorithm
	conf.HTTPServer.HTTPCors = []string{"*"}
	conf.HTTPServer.HTTPWhiteHost = []string{"*"}
	conf.P2PConfig.NetworkID = "net1"
	conf.ScdoConfig.GenesisConfig.Difficult = 1900000
	p2p.MergeBootnodes(&conf.P2PConfig)
	return conf
}

type heightSample struct {
	height uint64
	at     time.Time
}

func (s *session) statuses() []light.ShardStatus {
	paused, reason := light.SyncPause()
	out := make([]light.ShardStatus, 0, common.ShardCount)
	seen := make(map[uint]bool)
	for _, shard := range s.shards {
		svc := s.full[shard]
		if svc == nil {
			continue
		}
		out = append(out, s.fullStatus(shard, svc, paused, reason))
		seen[shard] = true
	}
	if s.api != nil {
		rows, err := s.api.Syncing()
		if err == nil {
			for _, row := range rows {
				headerDisk := DirBytes(LiteShardDir(s.dataDir, row.Shard))
				if seen[row.Shard] {
					for i := range out {
						if out[i].Shard == row.Shard {
							out[i].HeaderDiskBytes = headerDisk
						}
					}
					continue
				}
				row.DiskBytes = headerDisk
				row.HeaderDiskBytes = headerDisk
				row.Network = light.NetworkLabel()
				row.Paused = paused
				row.PauseReason = reason
				row.ETA = s.eta(row.Shard, row.Height, row.PeerHeight, paused, "")
				out = append(out, row)
			}
		}
	}
	return out
}

func (s *session) fullStatus(shard uint, svc *scdo.ScdoService, paused bool, reason string) light.ShardStatus {
	st := light.ShardStatus{
		Shard:           shard,
		Mode:            "full",
		ForkGenesis:     common.ScdoForkHeight,
		Paused:          paused,
		PauseReason:     reason,
		Network:         light.NetworkLabel(),
		DiskBytes:       DirBytes(ProShardDir(s.dataDir, shard)),
		HeaderDiskBytes: DirBytes(LiteShardDir(s.dataDir, shard)),
	}
	var height uint64
	if svc != nil && svc.BlockChain() != nil && svc.BlockChain().CurrentBlock() != nil && svc.BlockChain().CurrentBlock().Header != nil {
		height = svc.BlockChain().CurrentBlock().Header.Height
		st.Height = height
	}
	reported := ""
	if svc != nil && svc.Downloader() != nil {
		prog := svc.Downloader().Progress()
		st.PeerHeight = prog.Highest
		st.Peers = prog.Peers
		st.Syncing = prog.Syncing || prog.Highest > height
		reported = prog.ETA
	}
	st.ETA = s.eta(shard, height, st.PeerHeight, paused, reported)
	return st
}

func (s *session) eta(shard uint, height, peer uint64, paused bool, reported string) string {
	if paused {
		return "paused"
	}
	if peer > 0 && height >= peer {
		return "0s"
	}
	if reported != "" && reported != "unknown" && reported != "0s" {
		return reported
	}
	now := time.Now()
	prev, ok := s.samples[shard]
	if s.samples == nil {
		s.samples = make(map[uint]heightSample)
	}
	s.samples[shard] = heightSample{height: height, at: now}
	if !ok || height <= prev.height {
		if reported != "" {
			return reported
		}
		return "unknown"
	}
	elapsed := now.Sub(prev.at).Seconds()
	if elapsed < 1 {
		return "unknown"
	}
	rate := float64(height-prev.height) / elapsed
	if rate <= 0 || peer <= height {
		return "unknown"
	}
	secs := float64(peer-height) / rate
	return (time.Duration(secs * float64(time.Second))).Truncate(time.Second).String()
}
