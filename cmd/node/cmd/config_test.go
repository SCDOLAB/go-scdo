package cmd

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/stretchr/testify/assert"
)

func writeTestConfig(t *testing.T, dir, dataDir, ipcName, privateKey string) string {
	t.Helper()
	body := []byte(`{
  "basic": {
    "name": "scdo node2",
    "version": "2.0.0",
    "dataDir": "` + dataDir + `",
    "address": "127.0.0.1:55028",
    "coinbase": "",
    "algorithm": "zpow"
  },
  "p2p": {
    "privateKey": "` + privateKey + `",
    "staticNodes": [],
    "address": "0.0.0.0:39008",
    "networkID": "scdo"
  },
  "log": {"isDebug": false, "printLog": true},
  "httpServer": {"address": "127.0.0.1:65027", "crossorigins": ["*"], "whiteHost": ["*"]},
  "ipcconfig": {"name": "` + ipcName + `"},
  "genesis": {"difficult": 22, "shard": 1, "timestamp": 1596942480}
}`)
	path := filepath.Join(dir, "node.json")
	if err := ioutil.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDataDirHomeAndIPC(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDirFlag = ""

	cfgPath := writeTestConfig(t, t.TempDir(), "Snode1", "scdo1.ipc", "P2P_PRIVATE_KEY")
	cfg, err := LoadConfigFromFile(cfgPath, "", "")
	assert.Nil(t, err)
	wantData := filepath.Join(home, ".scdo", "Snode1")
	assert.Equal(t, wantData, cfg.BasicConfig.DataDir)
	assert.Equal(t, filepath.Join(wantData, "scdo1.ipc"), cfg.IpcConfig.PipeName)
	assert.NotNil(t, cfg.P2PConfig.PrivateKey)
	keyFile := filepath.Join(wantData, "p2p.key")
	assert.True(t, common.FileOrFolderExists(keyFile))

	info, err := os.Stat(keyFile)
	assert.Nil(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	again, err := LoadConfigFromFile(cfgPath, "", "")
	assert.Nil(t, err)
	assert.Equal(t, cfg.P2PConfig.PrivateKey.D, again.P2PConfig.PrivateKey.D)
}

func TestAbsoluteDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	abs := t.TempDir()
	dataDirFlag = abs
	defer func() { dataDirFlag = "" }()

	cfgPath := writeTestConfig(t, t.TempDir(), "Snode1", "", "")
	cfg, err := LoadConfigFromFile(cfgPath, "", "")
	assert.Nil(t, err)
	assert.Equal(t, abs, cfg.BasicConfig.DataDir)
	assert.Equal(t, filepath.Join(abs, "scdo.ipc"), cfg.IpcConfig.PipeName)
	assert.False(t, common.FileOrFolderExists(filepath.Join(home, ".scdo", "Snode1", "p2p.key")))
	assert.True(t, common.FileOrFolderExists(filepath.Join(abs, "p2p.key")))
}
