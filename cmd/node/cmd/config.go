/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/scdoproject/go-scdo/cmd/util"
	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/hexutil"
	"github.com/scdoproject/go-scdo/core"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/log/comm"
	"github.com/scdoproject/go-scdo/node"
	"github.com/scdoproject/go-scdo/p2p"
)

const (
	p2pKeyPlaceholder = "P2P_PRIVATE_KEY"
	p2pKeyFileName    = "p2p.key"
)

// GetConfigFromFile unmarshals the config from the given file
func GetConfigFromFile(filepath string) (*util.Config, error) {
	var config util.Config
	buff, err := ioutil.ReadFile(filepath)
	if err != nil {
		return &config, err
	}

	err = json.Unmarshal(buff, &config)
	return &config, err
}

// Cast cast RPC address to 0.0.0.0
// miner mehtods already have security-defence setting, 0.0.0.0 is ok (after mainnet matures and becomes stable, we can switch to 127.0.0.1)
func Cast(conf *node.Config) {
	endpoint := conf.BasicConfig.RPCAddr
	pos := strings.LastIndex(endpoint, ":")
	port := endpoint[pos+1:]
	endpoint = "0.0.0.0:" + port
	conf.BasicConfig.RPCAddr = endpoint
}

// LoadConfigFromFile gets node config from the given file
func LoadConfigFromFile(configFile string, accounts string, poolAccounts string) (*node.Config, error) {
	cmdConfig, err := GetConfigFromFile(configFile)
	if err != nil {
		return nil, err
	}

	if cmdConfig.GenesisConfig.CreateTimestamp == nil {
		return nil, errors.New("Failed to get genesis timestamp")
	}
	cmdConfig.GenesisConfig.Accounts, err = LoadAccountConfig(accounts)
	if err != nil {
		return nil, err
	}

	config := CopyConfig(cmdConfig)
	if dataDirFlag != "" {
		config.BasicConfig.DataDir = dataDirFlag
	}
	if dbCacheFlag > 0 {
		config.BasicConfig.DbCache = dbCacheFlag
	}
	rawDataDir := config.BasicConfig.DataDir
	config.BasicConfig.DataDir = common.ResolveDataDir(rawDataDir)
	logDirName := rawDataDir
	if logDirName == "" {
		logDirName = "scdo"
	}
	if filepath.IsAbs(logDirName) {
		logDirName = filepath.Base(logDirName)
	}
	convertIPCServerPath(cmdConfig, config)

	config.P2PConfig, err = GetP2pConfig(cmdConfig, config.BasicConfig.DataDir)
	if err != nil {
		return config, err
	}
	p2p.MergeBootnodes(&config.P2PConfig)

	if len(config.BasicConfig.Coinbase) > 0 {
		config.ScdoConfig.Coinbase = common.HexMustToAddres(config.BasicConfig.Coinbase)
	}

	if len(config.BasicConfig.PrivateKey) > 0 {
		config.ScdoConfig.CoinbasePrivateKey, err = crypto.LoadECDSAFromString(config.BasicConfig.PrivateKey)
		if err != nil {
			return config, err
		}
	}

	if len(poolAccounts) > 0 {
		config.ScdoConfig.CoinbaseList, err = LoadPoolAccountConfig(poolAccounts)
		if err != nil {
			return nil, err
		}
	}

	config.ScdoConfig.TxConf = *core.DefaultTxPoolConfig()
	config.ScdoConfig.GenesisConfig = cmdConfig.GenesisConfig
	comm.LogConfiguration.PrintLog = config.LogConfig.PrintLog
	comm.LogConfiguration.IsDebug = config.LogConfig.IsDebug
	comm.LogConfiguration.DataDir = logDirName
	return config, nil
}

// convertIPCServerPath places a relative IPC name inside the resolved data directory.
// An absolute path is kept. Windows named pipes stay under \\.\pipe\.
func convertIPCServerPath(cmdConfig *util.Config, config *node.Config) {
	name := cmdConfig.Ipcconfig.PipeName
	dataDir := config.BasicConfig.DataDir
	if name == "" {
		if runtime.GOOS == "windows" {
			config.IpcConfig.PipeName = common.WindowsPipeDir + "scdo.ipc"
		} else {
			config.IpcConfig.PipeName = filepath.Join(dataDir, "scdo.ipc")
		}
		return
	}
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(name, common.WindowsPipeDir) || filepath.IsAbs(name) {
			config.IpcConfig.PipeName = name
		} else {
			config.IpcConfig.PipeName = common.WindowsPipeDir + name
		}
		return
	}
	if filepath.IsAbs(name) {
		config.IpcConfig.PipeName = name
	} else {
		config.IpcConfig.PipeName = filepath.Join(dataDir, name)
	}
}

// CopyConfig copy Config from the given config
func CopyConfig(cmdConfig *util.Config) *node.Config {
	config := &node.Config{
		BasicConfig:    cmdConfig.BasicConfig,
		LogConfig:      cmdConfig.LogConfig,
		HTTPServer:     cmdConfig.HTTPServer,
		WSServerConfig: cmdConfig.WSServerConfig,
		P2PConfig:      cmdConfig.P2PConfig,
		ScdoConfig:     node.ScdoConfig{},
		MetricsConfig:  cmdConfig.MetricsConfig,
	}
	return config
}

func p2pKeyUnset(key string) bool {
	key = strings.TrimSpace(key)
	return key == "" || key == p2pKeyPlaceholder
}

// GetP2pConfig loads the node p2p key. An empty value or the template placeholder
// P2P_PRIVATE_KEY generates a unique key on first start and stores it in dataDir/p2p.key.
func GetP2pConfig(cmdConfig *util.Config, dataDir string) (p2p.Config, error) {
	if cmdConfig.P2PConfig.PrivateKey != nil {
		return cmdConfig.P2PConfig, nil
	}

	keyStr := strings.TrimSpace(cmdConfig.P2PConfig.SubPrivateKey)
	if !p2pKeyUnset(keyStr) {
		key, err := crypto.LoadECDSAFromString(keyStr)
		if err != nil {
			return cmdConfig.P2PConfig, err
		}
		cmdConfig.P2PConfig.PrivateKey = key
		return cmdConfig.P2PConfig, nil
	}

	if dataDir == "" {
		return cmdConfig.P2PConfig, errors.New("data directory is required to store the p2p key")
	}
	keyPath := filepath.Join(dataDir, p2pKeyFileName)
	if common.FileOrFolderExists(keyPath) {
		buff, err := ioutil.ReadFile(keyPath)
		if err != nil {
			return cmdConfig.P2PConfig, err
		}
		key, err := crypto.LoadECDSAFromString(strings.TrimSpace(string(buff)))
		if err != nil {
			return cmdConfig.P2PConfig, fmt.Errorf("failed to load p2p key %s: %s", keyPath, err)
		}
		cmdConfig.P2PConfig.PrivateKey = key
		return cmdConfig.P2PConfig, nil
	}

	key, err := crypto.GenerateKey()
	if err != nil {
		return cmdConfig.P2PConfig, err
	}
	encoded := hexutil.BytesToHex(crypto.FromECDSA(key))
	if err = common.SaveFile(keyPath, []byte(encoded)); err != nil {
		return cmdConfig.P2PConfig, err
	}
	if err = os.Chmod(keyPath, 0600); err != nil {
		return cmdConfig.P2PConfig, err
	}
	cmdConfig.P2PConfig.PrivateKey = key
	fmt.Printf("generated a new p2p node key at %s\n", keyPath)
	return cmdConfig.P2PConfig, nil
}

// LoadAccountConfig get accounts and balances from the given file
func LoadAccountConfig(account string) (map[common.Address]*big.Int, error) {
	result := make(map[common.Address]*big.Int)
	if account == "" {
		return result, nil
	}

	buff, err := ioutil.ReadFile(account)
	if err != nil {
		return result, err
	}

	err = json.Unmarshal(buff, &result)
	return result, err
}

// LoadPoolAccountConfig get accounts from the given file
func LoadPoolAccountConfig(account string) ([]common.Address, error) {
	addrMap := make(map[common.Address]*big.Int)
	var result []common.Address
	if account == "" {
		return result, nil
	}

	buff, err := ioutil.ReadFile(account)
	if err != nil {
		return result, err
	}

	err = json.Unmarshal(buff, &addrMap)

	for addr, _ := range addrMap {
		result = append(result, addr)
	}
	return result, err
}
