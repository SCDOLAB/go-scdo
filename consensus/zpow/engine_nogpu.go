//go:build !gpu

package zpow

import (
	"sync"

	"github.com/rcrowley/go-metrics"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/log"
)

// StartMiningGpu is a CPU fallback. The default node build does not link
// libgoGpuDet.so, so `node -v` and sync work without the GPU library.
// Rebuild with -tags gpu to enable GPU mining.
func (engine *ZpowEngine) StartMiningGpu(block *types.Block, seed uint64, min uint64, max uint64, result chan<- *types.Block, abort <-chan struct{},
	isNonceFound *int32, once *sync.Once, detrate metrics.Meter, logger *log.ScdoLog) {
	engine.log.Warn("GPU mining requested but this binary was built without -tags gpu; using CPU mining")
	engine.StartMining(block, seed, min, max, result, abort, isNonceFound, once, detrate, logger)
}
