//go:build gpu

package zpow

/*
#cgo LDFLAGS: -L. -L${SRCDIR} -lgoGpuDet -L/usr/lib/cuda/lib64 -lcudart -lstdc++ -Wl,-rpath,$$ORIGIN
void Determinant(int* hashBytes, double* retDets, int Blocks, int Threads, int mtrxSize, int hashBytesize, int Height);
*/
import "C"

import (
	"math/big"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/rcrowley/go-metrics"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/log"
)

// StartMiningGpu mines with libgoGpuDet. Build with -tags gpu and place
// libgoGpuDet.so next to the binary (rpath $ORIGIN).
func (engine *ZpowEngine) StartMiningGpu(block *types.Block, seed uint64, min uint64, max uint64, result chan<- *types.Block, abort <-chan struct{},
	isNonceFound *int32, once *sync.Once, detrate metrics.Meter, log *log.ScdoLog) {
	var nonce = seed
	var caltimes = int64(0)
	target := new(big.Float).SetInt(getMiningTarget(block.Header.Difficulty))
	header := block.Header.Clone()
	numBytes := 32
	dim := matrixDim
	blocks := engine.blocks
	threads := engine.blockthreads

	maxmum := float64(1000)

miner:
	for {
		select {
		case <-abort:
			logAbort(log)
			detrate.Mark(caltimes)
			break miner

		default:
			if atomic.LoadInt32(isNonceFound) != 0 {
				log.Debug("exit mining as nonce is found by other threads")
				break miner
			}

			caltimes++
			detrate.Mark(1)
			if caltimes == 0x7FFFFFFFFFFFFFFF {
				caltimes = 0
			}
			var chasharray = make([]C.int, blocks*threads*numBytes)
			var retDet = make([]C.double, blocks*threads)
			var chash *C.int = &chasharray[0]
			var retD *C.double = &retDet[0]

			k := 0

			for j := 0; j < blocks*threads; j++ {
				header.Witness = []byte(strconv.FormatUint(nonce+uint64(j), 10))
				hash := header.Hash()
				hashbyte := hash.Bytes()

				for i := 0; i < len(hashbyte); i++ {
					chasharray[k] = (C.int)(hashbyte[i])
					k++
				}
			}

			C.Determinant(chash, retD, C.int(blocks), C.int(threads), C.int(dim), C.int(32), C.int(header.Height))

			for j := 0; j < blocks*threads; j++ {
				restBig := big.NewFloat(float64(retDet[j]))
				if float64(retDet[j]) > maxmum {
					maxmum = float64(retDet[j])
				}
				if restBig.Cmp(target) >= 0 {
					once.Do(func() {
						header.Witness = []byte(strconv.FormatUint(uint64(j)+nonce, 10))
						block.Header = header
						block.HeaderHash = header.Hash()

						select {
						case <-abort:
							chasharray = nil
							retDet = nil
							logAbort(log)
						case result <- block:
							atomic.StoreInt32(isNonceFound, 1)

							log.Debug("found det:%e", restBig)
							log.Debug("target:%e", target)
							log.Debug("times2try:%d", caltimes)
						}
					})
					chasharray = nil
					retDet = nil
					break miner
				}
			}
			nonce = nonce + uint64(blocks*threads-1)
			chasharray = nil
			retDet = nil
			if nonce >= max {
				nonce = min
			}
			if nonce == seed-1 {
				select {
				case <-abort:
					logAbort(log)
				case result <- nil:
					log.Warn("nonce finding outage")
				}

				break miner
			}

			nonce++
		}
	}
}
