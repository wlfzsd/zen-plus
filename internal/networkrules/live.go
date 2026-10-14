package networkrules

// Live exception store (2026-10-13, batch-2 runtime correctness).
//
// After Compact the three main stores are frozen: serving goroutines read
// them without locks, so a runtime write would race. The whitelist server
// (internal/filter/whitelistserver) is the only runtime writer — users add
// allowlist rules from the block page while the proxy is serving. Those
// inserts land here instead: a small RWMutex-guarded store consulted after
// the frozen exception store on every exception lookup.
//
// The store reuses ruleStore for pattern semantics (tree/generic/regexp
// buckets); its result pool stays internal — appendInto copies candidates
// into the caller's pooled slice so putRes ownership is unchanged at the
// call sites.

import (
	"sync"
	"sync/atomic"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
)

type liveExceptionStore struct {
	mu    sync.RWMutex
	store *ruleStore[*exceptionrule.ExceptionRule]
	// n counts inserted rules; zero means appendInto is a no-op and the
	// serving hot path pays one atomic load.
	n atomic.Int64
}

func (s *liveExceptionStore) insert(pattern string, er *exceptionrule.ExceptionRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.Insert(pattern, er); err != nil {
		return err
	}
	s.n.Add(1)
	return nil
}

// appendInto appends candidates for url to the caller's result slice and
// returns it (possibly re-allocated by append). Callers keep their existing
// putRes ownership over the returned slice.
func (s *liveExceptionStore) appendInto(url string, res []*exceptionrule.ExceptionRule) []*exceptionrule.ExceptionRule {
	if s.n.Load() == 0 {
		return res
	}
	s.mu.RLock()
	sub := s.store.Get(url)
	res = append(res, sub...)
	s.store.putRes(sub)
	s.mu.RUnlock()
	return res
}
