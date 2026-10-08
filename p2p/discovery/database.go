/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/log"
)

// NodeHook some hook funcs
type NodeHook func(node *Node)

// Database definition
type Database struct {
	m              map[common.Hash]*Node
	log            *log.ScdoLog
	mutex          sync.RWMutex
	persist        sync.Mutex
	lastNodes      time.Time
	addNodeHook    NodeHook
	deleteNodeHook NodeHook
}

const (
	// minDiskSnapshotInterval is the fastest nodes.json or blockList.json may
	// be replaced. A shorter timer is clamped. Neither file is fsynced on this
	// timer; clean shutdown fsyncs each once.
	minDiskSnapshotInterval = 10 * time.Second

	// NodesBackupInterval is how often known peers are written to disk.
	// A restart dials this file immediately, so it has to stay current.
	// It must not be shorter than minDiskSnapshotInterval.
	NodesBackupInterval = time.Minute

	// NodesBackupFileName is the nodes info of backup file name
	NodesBackupFileName = "nodes.json"
)

// discoveryFsyncs counts fsyncs of nodes.json and blockList.json.
var discoveryFsyncs int64

// StartSaveNodes will save to a file and open a timer to backup the nodes info
func (db *Database) StartSaveNodes(nodeDir string, done chan struct{}) {
	interval := NodesBackupInterval
	if interval < minDiskSnapshotInterval {
		interval = minDiskSnapshotInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			go db.SaveNodes(nodeDir)
		case <-done:
			return
		}
	}
}

// SaveNodes dumps known peers. The timer calls this without an fsync.
func (db *Database) SaveNodes(nodeDir string) {
	db.writeNodes(nodeDir, false)
}

// SaveNodesDurable dumps known peers and fsyncs the file. Clean shutdown only.
func (db *Database) SaveNodesDurable(nodeDir string) {
	db.writeNodes(nodeDir, true)
}

func (db *Database) writeNodes(nodeDir string, durable bool) {
	if db == nil || nodeDir == "" {
		return
	}
	db.mutex.RLock()
	if db.m == nil {
		db.mutex.RUnlock()
		return
	}
	nodeStr := make([]string, 0, len(db.m))
	for _, v := range db.m {
		nodeStr = append(nodeStr, v.String())
	}
	db.mutex.RUnlock()

	db.persist.Lock()
	defer db.persist.Unlock()
	if !durable && !db.lastNodes.IsZero() && time.Since(db.lastNodes) < minDiskSnapshotInterval {
		return
	}

	nodeByte, err := json.MarshalIndent(nodeStr, "", "\t")
	if err != nil {
		db.log.Error("json marshal error, [%s]", err.Error())
		return
	}

	fileFullPath := filepath.Join(nodeDir, NodesBackupFileName)
	if err = replaceFile(fileFullPath, nodeByte, durable); err != nil {
		db.log.Error("nodes info backup failed, for:[%s]", err.Error())
		return
	}
	db.lastNodes = time.Now()
	db.log.Info("backups nodes. node length %d", len(nodeStr))
	db.log.Debug("nodes:%s info backup success\n", string(nodeByte))
}

// replaceFile writes path via a temp file and rename. durable fsyncs the
// temp file before the rename. The periodic path leaves durable false.
func replaceFile(path string, data []byte, durable bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil && durable {
		err = f.Sync()
		if err == nil {
			atomic.AddInt64(&discoveryFsyncs, 1)
		}
	}
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// NewDatabase new database
func NewDatabase(log *log.ScdoLog) *Database {
	return &Database{
		m:   make(map[common.Hash]*Node),
		log: log,
	}
}

func (db *Database) add(value *Node, notify bool) {
	db.mutex.Lock()
	defer db.mutex.Unlock()

	sha := value.getSha()
	if notify && db.addNodeHook != nil {
		go db.addNodeHook(value)
	}

	db.m[sha] = value
}

// FindByNodeID find node by its id
func (db *Database) FindByNodeID(id common.Address) (*Node, bool) {
	db.mutex.RLock()
	defer db.mutex.RUnlock()

	sha := crypto.HashBytes(id.Bytes())
	val, ok := db.m[sha]

	return val, ok
}

func (db *Database) delete(id common.Hash) {
	db.mutex.Lock()
	defer db.mutex.Unlock()

	if val, ok := db.m[id]; ok && db.deleteNodeHook != nil {
		go db.deleteNodeHook(val)
	}

	delete(db.m, id)
}

func (db *Database) getRandNodes(number int) []*Node {
	db.mutex.RLock()
	defer db.mutex.RUnlock()

	nodes := make([]*Node, 0)
	count := 0
	for _, value := range db.m {
		if count == number {
			break
		}

		nodes = append(nodes, value)
		count++
	}

	return nodes
}

func (db *Database) getRandNode() *Node {
	nodes := db.getRandNodes(1)
	if len(nodes) != 1 {
		return nil
	}

	return nodes[0]
}

func (db *Database) size() int {
	db.mutex.RLock()
	defer db.mutex.RUnlock()

	return len(db.m)
}

// GetCopy get replica from db.nodes
func (db *Database) GetCopy() map[common.Hash]*Node {
	db.mutex.RLock()
	defer db.mutex.RUnlock()

	copyMap := make(map[common.Hash]*Node)
	for key, value := range db.m {
		copyMap[key] = value
	}

	return copyMap
}

// SetHookForNewNode this hook will be called when find new Node
// Note it will run in a new go routine
func (db *Database) SetHookForNewNode(hook NodeHook) {
	db.addNodeHook = hook
}

// SetHookForDeleteNode this hook will be called when we lost a Node's connection
// Note it will run in a new go routine
func (db *Database) SetHookForDeleteNode(hook NodeHook) {
	db.deleteNodeHook = hook
}
