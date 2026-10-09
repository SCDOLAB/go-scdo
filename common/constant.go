/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package common

import (
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"
)

const (

	// ScdoProtoName protoName of Scdo service
	ScdoProtoName = "scdo"

	// ScdoVersion Version number of Scdo protocol
	ScdoVersion uint = 1

	// ScdoNodeVersion for simpler display
	ScdoNodeVersion string = "Scdo_V2.0.0"

	// ShardCount represents the total number of shards.
	ShardCount = 4

	// ShardByte represents the number of bytes used for shard information, must be smaller than 8
	ShardByte = 1

	// MetricsRefreshTime is the time of metrics sleep 1 minute
	MetricsRefreshTime = time.Minute

	// CPUMetricsRefreshTime is the time of metrics monitor cpu
	CPUMetricsRefreshTime = time.Second

	// ConfirmedBlockNumber is the block number for confirmed a block, it should be more than 12 in product.
	// 120 blocks is about 40 minutes at the ~20s target. Source-shard reorgs have removed
	// transactions after a debt that referenced them was already accepted, so 120 is not final.
	ConfirmedBlockNumber = 120

	// DebtIrreversibleConfirmations is the depth required once DebtIrreversibleForkHeight
	// is reached. 10_000 blocks is about 55 hours at the 20s target and about three days
	// at the ~30s interval measured on the public chain (heights 3_000_000 to 3_100_000).
	// That is the same window the header client already treats as covering ordinary reorgs.
	DebtIrreversibleConfirmations = 10000

	// DebtIrreversibleForkHeight is the first block that requires the deeper confirmation.
	// Zero means the fork is not scheduled. Turning it on is a coordinated network upgrade:
	// a node that requires 10_000 confirmations will not follow blocks the rest of the
	// network packed after only 120.
	DebtIrreversibleForkHeight uint64 = 0

	ScdoForkHeight = 2979594

	// emery hard fork: update zpow consensus and evm
	EmeryForkHeight = ScdoForkHeight

	// ForkHeight after this height we change the content of block: hardFork
	ForkHeight = ScdoForkHeight

	// ForkHeight after this height we change the content of block: hardFork
	SecondForkHeight = ScdoForkHeight

	// ForkHeight after this height we change the validation of tx: hardFork
	ThirdForkHeight = ScdoForkHeight

	SmartContractNonceForkHeight = ScdoForkHeight

	// SmartContractNonceFixHeight fix smart contract nonce bug when user use setNonce
	SmartContractNonceFixHeight = ScdoForkHeight

	// LightChainDir lightchain data directory based on config.DataRoot
	LightChainDir = "/db/lightchain"

	// Sha256Algorithm miner algorithm sha256
	Sha256Algorithm = "sha256"

	// zpow miner algorithm
	ZpowAlgorithm = "zpow"

	// BFT mineralgorithm
	BFTEngine = "bft"

	// BFT data folder
	BFTDataFolder = "bftdata"

	// EVMStackLimit increase evm stack limit to 8192
	EVMStackLimit = 8192

	// BlockPackInterval it's an estimate time.
	BlockPackInterval = 15 * time.Second

	// Height: fix the issue caused by forking from collapse database
	HeightFloor = uint64(707989)
	HeightRoof  = uint64(707996)

	WindowsPipeDir = `\\.\pipe\`

	defaultPipeFile = `\scdo.ipc`
)

var (
	// tempFolder used to store temp file, such as log files
	tempFolder string

	// defaultDataFolder used to store persistent data info, such as the database and keystore
	defaultDataFolder string

	// defaultIPCPath used to store the ipc file
	defaultIPCPath string
)

// Common big integers often used
var (
	Big1   = big.NewInt(1)
	Big2   = big.NewInt(2)
	Big3   = big.NewInt(3)
	Big0   = big.NewInt(0)
	Big32  = big.NewInt(32)
	Big256 = big.NewInt(256)
	Big257 = big.NewInt(257)
)

// HomeDir returns the user's home directory.
// $HOME (and %USERPROFILE% on Windows) wins over the passwd entry so a
// process started with HOME set elsewhere does not open /home/<user>/.scdo.
func HomeDir() string {
	if runtime.GOOS == "windows" {
		if p := os.Getenv("USERPROFILE"); p != "" {
			return p
		}
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	usr, err := user.Current()
	if err != nil {
		panic(err)
	}
	return usr.HomeDir
}

// init initialize the paths to store data
func init() {
	refreshDefaultPaths()
}

func refreshDefaultPaths() {
	home := HomeDir()
	tempFolder = filepath.Join(home, "scdoTemp")
	defaultDataFolder = filepath.Join(home, ".scdo")

	if runtime.GOOS == "windows" {
		defaultIPCPath = WindowsPipeDir + "scdo.ipc"
	} else {
		defaultIPCPath = filepath.Join(defaultDataFolder, "scdo.ipc")
	}
}

// GetTempFolder gets the temp folder
func GetTempFolder() string {
	refreshDefaultPaths()
	return tempFolder
}

// GetDefaultDataFolder gets the default data Folder
func GetDefaultDataFolder() string {
	refreshDefaultPaths()
	return defaultDataFolder
}

// GetDefaultIPCPath gets the default IPC path
func GetDefaultIPCPath() string {
	refreshDefaultPaths()
	return defaultIPCPath
}

// ResolveDataDir returns an absolute data directory.
// An absolute path is used as-is. A relative path is placed under $HOME/.scdo.
// An empty path is $HOME/.scdo.
func ResolveDataDir(dataDir string) string {
	if dataDir == "" {
		return GetDefaultDataFolder()
	}
	if filepath.IsAbs(dataDir) {
		return dataDir
	}
	return filepath.Join(GetDefaultDataFolder(), dataDir)
}
