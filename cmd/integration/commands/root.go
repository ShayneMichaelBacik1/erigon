package commands

import (
	"path/filepath"
	"runtime"

	"github.com/c2h5oh/datasize"
	"github.com/ledgerwatch/erigon-lib/kv"
	kv2 "github.com/ledgerwatch/erigon-lib/kv/mdbx"
	"github.com/ledgerwatch/erigon/cmd/utils"
	debug2 "github.com/ledgerwatch/erigon/common/debug"
	"github.com/ledgerwatch/erigon/migrations"
	"github.com/ledgerwatch/erigon/turbo/debug"
	"github.com/ledgerwatch/erigon/turbo/logging"
	"github.com/ledgerwatch/log/v3"
	"github.com/spf13/cobra"
	"github.com/torquem-ch/mdbx-go/mdbx"
	"golang.org/x/sync/semaphore"
)

var rootCmd = &cobra.Command{
	Use:   "integration",
	Short: "long and heavy integration tests for Erigon",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if err := debug.SetupCobra(cmd); err != nil {
			panic(err)
		}
		if chaindata == "" {
			chaindata = filepath.Join(datadirCli, "chaindata")
		}
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		defer debug.Exit()
	},
}

func RootCommand() *cobra.Command {
	utils.CobraFlags(rootCmd, debug.Flags, utils.MetricFlags, logging.Flags)
	return rootCmd
}

func dbCfg(label kv.Label, path string) kv2.MdbxOpts {
	limiterB := semaphore.NewWeighted(int64(runtime.NumCPU()*10 + 1))
	opts := kv2.NewMDBX(log.New()).Path(path).Label(label).RoTxsLimiter(limiterB)
	if label == kv.ChainDB {
		opts = opts.MapSize(8 * datasize.TB)
	}
	if databaseVerbosity != -1 {
		opts = opts.DBVerbosity(kv.DBVerbosityLvl(databaseVerbosity))
	}
	return opts
}

func openDB(opts kv2.MdbxOpts, applyMigrations bool) kv.RwDB {
	// integration tool don't intent to create db, then easiest way to open db - it's pass mdbx.Accede flag, which allow
	// to read all options from DB, instead of overriding them
	opts = opts.Flags(func(f uint) uint { return f | mdbx.Accede })

	if debug2.WriteMap() {
		log.Info("[db] Enabling WriteMap")
		opts = opts.WriteMap()
	}
	if debug2.MergeTr() > 0 {
		log.Info("[db] Setting", "MergeThreshold", debug2.MergeTr())
		opts = opts.WriteMergeThreshold(uint64(debug2.MergeTr() * 8192))
	}
	if debug2.MdbxReadAhead() {
		log.Info("[db] Setting Enabling ReadAhead")
		opts = opts.Flags(func(u uint) uint {
			return u &^ mdbx.NoReadahead
		})
	}

	db := opts.MustOpen()
	if applyMigrations {
		migrator := migrations.NewMigrator(opts.GetLabel())
		has, err := migrator.HasPendingMigrations(db)
		if err != nil {
			panic(err)
		}
		if has {
			log.Info("Re-Opening DB in exclusive mode to apply DB migrations")
			db.Close()
			db = opts.Exclusive().MustOpen()
			if err := migrator.Apply(db, datadirCli); err != nil {
				panic(err)
			}
			db.Close()
			db = opts.MustOpen()
		}
	}
	return db
}
