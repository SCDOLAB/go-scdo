/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package downloader

// PrivatedownloaderAPI provides an API to access downloader information.
type PrivatedownloaderAPI struct {
	d *Downloader
}

// NewPrivatedownloaderAPI creates a new PrivatedownloaderAPI object for rpc service.
func NewPrivatedownloaderAPI(d *Downloader) *PrivatedownloaderAPI {
	return &PrivatedownloaderAPI{d}
}

// ShardSync is one shard's sync height. mode is "full" for this node and
// "headers" for the other shards' light clients.
type ShardSync struct {
	Shard  uint   `json:"shard"`
	Height uint64 `json:"height"`
	Mode   string `json:"mode"`
	Peers  int    `json:"peers"`
}

// SyncProgress is the scdo_syncing result used by the wallet overlay.
type SyncProgress struct {
	Syncing           bool        `json:"syncing"`
	Current           uint64      `json:"current"`
	Highest           uint64      `json:"highest"`
	BlocksPerSec      float64     `json:"blkPerSec"`
	ETA               string      `json:"eta"`
	Peers             int         `json:"peers"`
	WaitingOn         string      `json:"waitingOn,omitempty"`
	ConfirmationsNeed uint64      `json:"confirmationsNeed,omitempty"`
	ConfirmationsHave uint64      `json:"confirmationsHave,omitempty"`
	Shards            []ShardSync `json:"shards,omitempty"`
}

// SyncInfo sync information for current downloader sessoin.
type SyncInfo struct {
	Status     string // readable string of downloader.syncStatus
	Duration   string // duration in seconds
	StartNum   uint64 // start block number
	Amount     uint64 // amount of blocks need to download
	Downloaded uint64
}

// GetStatus gets the SyncInfo.
func (api *PrivatedownloaderAPI) GetStatus() *SyncInfo {
	var info SyncInfo
	api.d.getSyncInfo(&info)

	result := SyncInfo{
		Status:     info.Status,
		Duration:   info.Duration,
		StartNum:   info.StartNum,
		Amount:     info.Amount,
		Downloaded: info.Downloaded,
	}

	return &result
}

func (api *PrivatedownloaderAPI) IsSyncing() bool {
	return api.d.syncStatus != statusNone
}
