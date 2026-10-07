/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package leveldb

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/syndtr/goleveldb/leveldb/opt"
)

func TestAutoCacheMB(t *testing.T) {
	dir := t.TempDir()
	if got := AutoCacheMB(dir); got != InitialSyncCacheMB {
		t.Fatalf("empty dir cache = %d, want %d", got, InitialSyncCacheMB)
	}
	if got := AutoCacheMB(filepath.Join(dir, "missing")); got != InitialSyncCacheMB {
		t.Fatalf("missing dir cache = %d, want %d", got, InitialSyncCacheMB)
	}

	big := filepath.Join(dir, "grown")
	if err := os.MkdirAll(big, 0700); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024*1024)
	for i := 0; i < 65; i++ {
		if err := os.WriteFile(filepath.Join(big, fmt.Sprintf("f%02d", i)), buf, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := AutoCacheMB(big); got != SteadyCacheMB {
		t.Fatalf("grown dir cache = %d, want %d", got, SteadyCacheMB)
	}
}

func TestCacheOptionsScalesWriteBuffer(t *testing.T) {
	opts := cacheOptions(InitialSyncCacheMB)
	if opts.BlockCacheCapacity != InitialSyncCacheMB*opt.MiB/2 {
		t.Fatalf("block cache = %d", opts.BlockCacheCapacity)
	}
	if opts.WriteBuffer != 128*opt.MiB {
		t.Fatalf("write buffer = %d", opts.WriteBuffer)
	}
}

func BenchmarkBatchWrite(b *testing.B) {
	for _, mb := range []int{8, InitialSyncCacheMB} {
		b.Run(fmt.Sprintf("%dMB", mb), func(b *testing.B) {
			dir := b.TempDir()
			db, err := NewLevelDBWithCache(dir, mb)
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			payload := make([]byte, 4096)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				batch := db.NewBatch()
				for k := 0; k < 32; k++ {
					key := []byte(fmt.Sprintf("b-%d-%d", i, k))
					batch.Put(key, payload)
				}
				if err := batch.Commit(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestNewLevelDBWithCacheBatch(t *testing.T) {
	dir := t.TempDir()
	db, err := NewLevelDBWithCache(dir, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	batch := db.NewBatch()
	for i := 0; i < 100; i++ {
		batch.Put([]byte{byte(i)}, []byte("block"))
	}
	if err := batch.Commit(); err != nil {
		t.Fatal(err)
	}
	ok, err := db.Has([]byte{1})
	if err != nil || !ok {
		t.Fatalf("batch write missing, ok=%v err=%v", ok, err)
	}
}
