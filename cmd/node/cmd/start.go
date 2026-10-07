/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/consensus"
	"github.com/scdoproject/go-scdo/consensus/factory"
	"github.com/scdoproject/go-scdo/light"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/log/comm"
	"github.com/scdoproject/go-scdo/metrics"
	miner2 "github.com/scdoproject/go-scdo/miner"
	"github.com/scdoproject/go-scdo/monitor"
	"github.com/scdoproject/go-scdo/node"
	"github.com/scdoproject/go-scdo/scdo"
	"github.com/scdoproject/go-scdo/scdo/lightclients"
	"github.com/spf13/cobra"
)

var (
	scdoNodeConfigFile string
	miner              string
	metricsEnableFlag  bool
	accountsConfig     string
	poolAccountsConfig string
	threads            int
	startHeight        int
	isPoolMode         bool
	threadblocks       int //thread blocks, i.e, gridDim.x
	blockthreads       int //number threads per block, threadDim.x
	// default is full node
	lightNode bool

	// lightServer serves phone light clients on the full node's TCP port.
	// Default on, so every newcomer node can feed phones.
	lightServer bool

	//pprofPort http server port
	pprofPort uint64

	// profileSize is used to limit when need to collect profiles, set 6GB
	profileSize = uint64(1024 * 1024 * 1024 * 6)

	maxConns       = int(0)
	maxActiveConns = int(0)

	// dataDirFlag overrides basic.dataDir. Absolute paths are used as-is.
	dataDirFlag string

	// dbCacheFlag overrides basic.dbcache, in megabytes. 0 keeps the automatic size.
	dbCacheFlag int
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "start the node of scdo",
	Long: `usage example:
		node.exe start -c cmd\node.json
		start a node.`,

	Run: func(cmd *cobra.Command, args []string) {
		var wg sync.WaitGroup
		nCfg, err := LoadConfigFromFile(scdoNodeConfigFile, accountsConfig, poolAccountsConfig)
		if err != nil {
			fmt.Printf("failed to reading the config file: %s\n", err.Error())
			return
		}
		Cast(nCfg)
		minerInfo := strings.ToLower(miner)
		if len(nCfg.BasicConfig.Coinbase) == 0 {
			if minerInfo == "start" {
				fmt.Println("Mining needs a coinbase. Create a wallet key, then put the address in basic.coinbase:")
				fmt.Println("  node key --shard N")
				fmt.Println("N is 1, 2, 3 or 4. Keep the private key safe. Mining rewards go to that address.")
				fmt.Println("To sync without mining, omit -m start.")
				return
			}
			fmt.Println("No coinbase is set. Syncing without mining. Set basic.coinbase before -m start.")
		}
		if !comm.LogConfiguration.PrintLog {
			fmt.Printf("log folder: %s\n", filepath.Join(log.LogFolder, comm.LogConfiguration.DataDir))
		}

		scdoNode, err := node.New(nCfg)
		if err != nil {
			fmt.Println(err.Error())
			return
		}

		// Create scdo service and register the service
		scdolog := log.GetLogger("scdo")
		lightLog := log.GetLogger("scdo-light")
		serviceContext := scdo.ServiceContext{
			DataDir: nCfg.BasicConfig.DataDir,
		}
		ctx := context.WithValue(context.Background(), "ServiceContext", serviceContext)

		var engine consensus.Engine
		if nCfg.BasicConfig.MinerAlgorithm == common.BFTEngine {
			engine, err = factory.GetBFTEngine(nCfg.ScdoConfig.CoinbasePrivateKey, nCfg.BasicConfig.DataDir)
		} else {
			engine, err = factory.GetConsensusEngine(nCfg.BasicConfig.MinerAlgorithm)
		}

		if err != nil {
			fmt.Println(err)
			return
		}

		// start pprof http server
		if pprofPort > 0 {
			go func() {
				if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", pprofPort), nil); err != nil {
					fmt.Println("Failed to start pprof http server,", err)
					return
				}
			}()
		}

		if comm.LogConfiguration.IsDebug {
			go monitorPC()
		}

		if lightNode {
			lightService, err := light.NewServiceClient(ctx, nCfg, lightLog, common.LightChainDir, scdoNode.GetShardNumber(), engine)
			if err != nil {
				fmt.Println("Create light service error.", err.Error())
				return
			}

			if err := scdoNode.Register(lightService); err != nil {
				fmt.Println(err.Error())
				return
			}

			err = scdoNode.Start()
			if err != nil {
				fmt.Printf("got error when start node: %s\n", err)
				return
			}
		} else {
			// light client manager
			manager, err := lightclients.NewLightClientManager(scdoNode.GetShardNumber(), ctx, nCfg, engine)
			if err != nil {
				fmt.Printf("create light client manager failed. %s", err)
				return
			}

			// fullnode mode
			scdoService, err := scdo.NewScdoService(ctx, nCfg, scdolog, engine, manager, startHeight, isPoolMode)
			if err != nil {
				fmt.Println(err.Error())
				return
			}

			scdoService.Miner().SetThreads(threads)

			scdoService.Miner().SetGpuBlocksThreads(threadblocks, blockthreads)

			// Disable mining before the node starts syncing. Otherwise every
			// downloader and tx event tries to start the miner and logs
			// "cannot start miner,stopper:1".
			if minerInfo == "" || minerInfo == "stop" {
				scdoService.Miner().SetStopper(1)
			}

			// monitor service
			monitorService, err := monitor.NewMonitorService(scdoService, scdoNode, nCfg, scdolog, "Test monitor")
			if err != nil {
				fmt.Println(err.Error())
				return
			}

			services := manager.GetServices()
			services = append(services, scdoService, monitorService)
			shard := scdoNode.GetShardNumber()
			if lightServer {
				lightServerService, err := light.NewServiceServer(scdoService, nCfg, lightLog, shard)
				if err != nil {
					fmt.Println("Create light server err. ", err.Error())
					return
				}
				services = append(services, lightServerService)
				fmt.Printf("Light server on (default). Protocol %s_%d version %d on TCP/UDP %s, the same port as full sync. Pass --lightserver=false to disable.\n",
					light.LightProtoName, shard, light.LightScdoVersion, nCfg.P2PConfig.ListenAddr)
			} else {
				fmt.Println("Light server off (--lightserver=false). Phones cannot header-sync from this node.")
			}
			for _, service := range services {
				if err := scdoNode.Register(service); err != nil {
					fmt.Println(err.Error())
					return
				}
			}

			err = scdoNode.Start()
			if maxConns > 0 {
				scdoService.P2PServer().SetMaxConnections(maxConns)
			}
			if maxActiveConns > 0 {
				scdoService.P2PServer().SetMaxActiveConnections(maxActiveConns)
			}
			if err != nil {
				fmt.Printf("got error when start node: %s\n", err)
				return
			}
			if lightServer && scdoService.P2PServer() != nil && scdoService.P2PServer().SelfNode != nil {
				fmt.Printf("Phone bootnode: %s\n", scdoService.P2PServer().SelfNode)
				fmt.Println("If that host is 0.0.0.0, substitute this machine's LAN address. On the .50 test network that is 192.168.50.50.")
			}

			if minerInfo == "start" {
				err = scdoService.Miner().Start()
				if err != nil && err != miner2.ErrMinerIsRunning {
					fmt.Println("failed to start the miner : ", err)
					return
				}
			} else if minerInfo == "" || minerInfo == "stop" {
				scdoService.Miner().SetStopper(1)
				scdoService.Miner().Stop()
			} else {
				fmt.Println("invalid miner command, must be start or stop")
				return
			}
		}

		if metricsEnableFlag {
			metrics.StartMetricsWithConfig(
				nCfg.MetricsConfig,
				scdolog,
				nCfg.BasicConfig.Name,
				nCfg.BasicConfig.Version,
				nCfg.P2PConfig.NetworkID,
				nCfg.ScdoConfig.Coinbase,
			)
		}

		wg.Add(1)
		wg.Wait()
	},
}

func init() {
	rootCmd.AddCommand(startCmd)

	startCmd.Flags().StringVarP(&scdoNodeConfigFile, "config", "c", "", "scdo node config file (required)")
	startCmd.MustMarkFlagRequired("config")

	startCmd.Flags().StringVarP(&miner, "miner", "m", "stop", "miner start or not, [start, stop]. Default is stop (sync only)")
	startCmd.Flags().StringVar(&dataDirFlag, "datadir", "", "data directory. Absolute paths are used as-is; relative paths are placed under $HOME/.scdo. Overrides basic.dataDir")
	startCmd.Flags().IntVar(&dbCacheFlag, "dbcache", 0, "chain database cache in MB. 0 uses 512 during initial sync and 128 once the chain database is larger")
	startCmd.Flags().BoolVarP(&metricsEnableFlag, "metrics", "t", false, "start metrics")
	startCmd.Flags().StringVarP(&accountsConfig, "accounts", "", "", "init accounts info")
	startCmd.Flags().StringVarP(&poolAccountsConfig, "poolaccounts", "", "", "init pool accounts")
	startCmd.Flags().IntVarP(&threads, "threads", "", 1, "miner thread value")
	startCmd.Flags().BoolVarP(&lightNode, "light", "l", false, "whether start with light mode")
	startCmd.Flags().BoolVar(&lightServer, "lightserver", true, "serve phone light clients (lightScdo_<shard> version 1) on this node's TCP port. On by default")
	startCmd.Flags().Uint64VarP(&pprofPort, "port", "", 0, "which port pprof http server listen to")
	startCmd.Flags().IntVarP(&startHeight, "startheight", "", -1, "the block height to start from")
	startCmd.Flags().IntVarP(&maxConns, "maxConns", "", 0, "node max connections")
	startCmd.Flags().IntVarP(&maxActiveConns, "maxActiveConns", "", 0, "node max active connections")
	startCmd.Flags().BoolVarP(&isPoolMode, "pool", "", false, "pool mode")
	startCmd.Flags().IntVarP(&threadblocks, "threadblocks", "", 0, "number of thread blocks in a gpu device")
	startCmd.Flags().IntVarP(&blockthreads, "blockthreads", "", 1, "number of threads per block in a gpu device")

}

func monitorPC() {
	var info runtime.MemStats
	heapDir := filepath.Join(common.GetTempFolder(), "heapProfile")
	err := os.MkdirAll(heapDir, os.ModePerm)
	if err != nil {
		fmt.Printf("failed to create folder %s: %s\n", heapDir, err)
		return
	}

	profileDir := filepath.Join(common.GetTempFolder(), "cpuProfile")
	err = os.MkdirAll(profileDir, os.ModePerm)
	if err != nil {
		fmt.Printf("failed to create folder %s: %s\n", profileDir, err)
		return
	}

	ticker := time.NewTicker(1 * time.Minute)
	for {
		select {
		case <-ticker.C:
			runtime.ReadMemStats(&info)
			if info.Alloc > profileSize {
				heapFile := filepath.Join(heapDir, fmt.Sprint("heap-", time.Now().Format("2006-01-02-15-04-05")))
				f, err := os.Create(heapFile)
				if err != nil {
					fmt.Println("monitor create heap file err:", err)
					return
				}
				err = pprof.WriteHeapProfile(f)
				if err != nil {
					fmt.Println("monitor write heap file err:", err)
					return
				}

				profileFile := filepath.Join(profileDir, fmt.Sprint("cpu-", time.Now().Format("2006-01-02-15-04-05")))
				cpuf, err := os.Create(profileFile)
				if err != nil {
					fmt.Println("monitor create cpu file err:", err)
					return
				}

				if err := pprof.StartCPUProfile(cpuf); err != nil {
					fmt.Println("failed to start cpu profile err:", err)
					return
				}

				time.Sleep(20 * time.Second)
				pprof.StopCPUProfile()
			}
		}
	}
}
