package networkrules

import (
	"fmt"
	"sync"
	"testing"
)

// Regression test for the parallel-load data races (2026-10-13): list loading
// runs one goroutine per filter list (internal/app populateFilter), and the
// shared buckets that ParseRule feeds must tolerate concurrent writes. Run
// with -race; without the store locks this crashes (concurrent map writes)
// or tears slices (generic/regexp appends, badfilter appends).
//
// The rule mix deliberately exercises every shared write path:
//   - tree patterns (||host^) → ruletree Insert (own insertMu)
//   - empty-pattern action rules ($cookie=/) → actionIdx.addRule (mu)
//   - empty-pattern non-action rules ($csp=) → ruleStore generic append (loadMu)
//   - /regexp/ rules → ruleStore regexp append (loadMu)
//   - $badfilter → badfilters append (bfMu)
func TestParseRuleParallelLoadNoRace(t *testing.T) {
	const workers = 8
	const perWorker = 120

	nr := New()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				lines := []string{
					fmt.Sprintf("||race-%d-%d.example.invalid^", w, i),
					"$cookie=race",
					"$removeparam=utm_race",
					"$csp=script-src 'none'",
					fmt.Sprintf("/race%d%d\\.js$/", w, i),
					fmt.Sprintf("||race-bf-%d-%d.example.invalid^$badfilter", w, i),
				}
				for _, line := range lines {
					if _, err := nr.ParseRule(line, nil); err != nil {
						t.Errorf("ParseRule(%q): %v", line, err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// All writes must have landed.
	nr.Compact()
	if got := len(nr.primaryStore.generic); got != workers*perWorker {
		t.Errorf("generic rules = %d, want %d", got, workers*perWorker)
	}
	if got := len(nr.primaryStore.regexp); got != workers*perWorker {
		t.Errorf("regexp rules = %d, want %d", got, workers*perWorker)
	}
	if nr.actionIdx.total != workers*perWorker*2 {
		t.Errorf("actionIdx total = %d, want %d", nr.actionIdx.total, workers*perWorker*2)
	}
	if got := len(nr.badfilters); got != workers*perWorker {
		t.Errorf("badfilters = %d, want %d", got, workers*perWorker)
	}
}
