package nrprobe

// Concurrency safety exercise for the probe 2 pooled paths (run under -race):
// GetLP/GetIdxLP share pooled accumulators/maps and pooled result slices;
// production ModifyReq can be called from multiple proxy goroutines.

import (
	"sync"
	"testing"
)

func TestGetLPParallelSafe(t *testing.T) {
	nr := probeBuildReal(t)
	urls := probeURLs(t, 500)
	if len(urls) == 0 {
		t.Fatal("no urls")
	}
	// warm the index so workers only exercise Get paths
	_ = nr.primaryStore.GetIdxLP(urls[0])

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				u := urls[(i+worker)%len(urls)]
				switch (i + worker) % 4 {
				case 0:
					_ = nr.primaryStore.Get(u)
				case 1:
					_ = nr.primaryStore.GetIdx(u)
				case 2:
					r := nr.primaryStore.GetLP(u)
					nr.primaryStore.putRes(r)
				case 3:
					r := nr.primaryStore.GetIdxLP(u)
					nr.primaryStore.putRes(r)
					re := nr.exceptionStore.GetIdxLP(u)
					nr.exceptionStore.putRes(re)
				}
			}
		}(w)
	}
	wg.Wait()
}
