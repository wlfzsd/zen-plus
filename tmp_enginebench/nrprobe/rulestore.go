package nrprobe

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	ruletreelp "github.com/irbis-sh/zen-desktop/tmp_enginebench/ruletreelp"
	"github.com/irbis-sh/zen-desktop/internal/trimslice"
)

// ruleStore matches URLs against ad-block style patterns with associated data.
type ruleStore[T comparable] struct {
	tree *ruletreelp.Tree[T]
	// generic is rules with an empty pattern, which match for every URL.
	generic []T
	regexp  []regexpRule[T]

	// PROBE (tmp_enginebench only): lazily built rarest-token reverse index
	// used by GetIdx; nil means not built yet. Production Get ignores it.
	idx atomic.Pointer[reIdx]

	// PROBE fields for probe 2 (low-alloc) and probe 3 (fastshape off):
	// resPool backs GetLP/GetIdxLP result slices; disableFast forces every
	// regexp rule through the regexp engine (fastshape matchers ignored).
	resOnce     sync.Once
	resPool     sync.Pool
	disableFast bool
}

const maxPooledResEntries = 8192 // drop oversized slices from the pool

// putRes returns a result slice's backing to the store pool (probe 2).
func (s *ruleStore[T]) putRes(res []T) {
	if res == nil || cap(res) > maxPooledResEntries {
		return
	}
	s.resOnce.Do(func() {
		s.resPool.New = func() any { return new([]T) }
	})
	b := res[:0]
	s.resPool.Put(&b)
}

type regexpRule[T comparable] struct {
	regexp *regexp.Regexp
	// fast is an exact-equivalence matcher for supported pattern shapes
	// (see fastshape.go); nil means the pattern is outside the family and
	// the regexp is always used.
	fast  *fastShape
	value T
}

// match reports whether url matches this rule. hasNL tells whether url
// contains '\n': an unflagged "." in the pattern cannot match '\n' while
// fastShape's gaps are position-free, so fastShape is only used for
// '\n'-free URLs (see fastshape.go for the exactness argument).
func (r *regexpRule[T]) match(url string, hasNL bool) bool {
	if r.fast != nil && !hasNL {
		return r.fast.matchFast(url)
	}
	return r.regexp.MatchString(url)
}

func newRuleStore[T comparable]() *ruleStore[T] {
	return &ruleStore[T]{
		tree: ruletreelp.New[T](),
	}
}

func (s *ruleStore[T]) Insert(pattern string, v T) error {
	// The pattern is either generic (empty), regexp, or regular.
	switch {
	case pattern == "":
		s.generic = append(s.generic, v)
	case len(pattern) > 1 && pattern[0] == '/' && pattern[len(pattern)-1] == '/':
		body := pattern[1 : len(pattern)-1]
		if body == "" {
			return fmt.Errorf("empty regexp rule")
		}
		re, err := regexp.Compile(body)
		if err != nil {
			return fmt.Errorf("compile regexp rule: %w", err)
		}
		s.regexp = append(s.regexp, regexpRule[T]{
			regexp: re,
			fast:   parseFastShape(body, re),
			value:  v,
		})
	default:
		s.tree.Insert(pattern, v)
	}

	return nil
}

func (s *ruleStore[T]) Get(url string) []T {
	matches := s.tree.Get(url)
	res := make([]T, 0, len(s.generic)+len(matches))
	res = append(res, s.generic...)
	res = append(res, matches...)
	if len(s.regexp) > 0 {
		// fastShape gaps are position-free while an unflagged "." cannot
		// match '\n', so '\n'-bearing URLs use the regexp for every rule
		// (one IndexByte per Get; see fastshape.go).
		hasNL := strings.IndexByte(url, '\n') >= 0
		for _, rule := range s.regexp {
			if rule.match(url, hasNL) {
				res = append(res, rule.value)
			}
		}
	}
	return res
}

// matchRule is match() with the probe-3 disableFast override.
func (s *ruleStore[T]) matchRule(r *regexpRule[T], url string, hasNL bool) bool {
	if !s.disableFast {
		if r.fast != nil && !hasNL {
			return r.fast.matchFast(url)
		}
		return r.regexp.MatchString(url)
	}
	_ = r.fast
	return r.regexp.MatchString(url)
}

// getProbe is the shared body of the probe variants.
//   - useIdx: rarest-token reverse index pruning (probe 1)
//   - lowAlloc: tree.GetLP + pooled accumulator/map + pooled result slice,
//     caller MUST putRes the returned slice exactly once (probe 2)
func (s *ruleStore[T]) getProbe(url string, useIdx, lowAlloc bool) []T {
	var matches []T
	if lowAlloc {
		matches = s.tree.GetLP(url)
	} else {
		matches = s.tree.Get(url)
	}
	var res []T
	if lowAlloc {
		s.resOnce.Do(func() {
			s.resPool.New = func() any { return new([]T) }
		})
		if b := s.resPool.Get(); b != nil {
			res = (*b.(*[]T))[:0]
		}
	}
	if res == nil {
		res = make([]T, 0, len(s.generic)+len(matches))
	}
	res = append(res, s.generic...)
	res = append(res, matches...)
	if len(s.regexp) > 0 {
		hasNL := strings.IndexByte(url, 10) >= 0
		if useIdx {
			idx := s.idx.Load()
			if idx == nil {
				n := s.buildIdx()
				if s.idx.CompareAndSwap(nil, n) {
					idx = n
				} else {
					idx = s.idx.Load()
				}
			}
			if idx != nil && len(s.regexp) <= idxMaxRules {
				var hit hitBits
				idx.markCandidates(url, &hit)
				for i := range s.regexp {
					if toks := idx.tokensOf[i]; len(toks) > 0 {
						if hit[i/64]&(1<<(i%64)) == 0 {
							continue
						}
						present := false
						for _, tk := range toks {
							if strings.Contains(url, tk) {
								present = true
								break
							}
						}
						if !present {
							continue
						}
					}
					if s.matchRule(&s.regexp[i], url, hasNL) {
						res = append(res, s.regexp[i].value)
					}
				}
				return res
			}
		}
		for i := range s.regexp {
			if s.matchRule(&s.regexp[i], url, hasNL) {
				res = append(res, s.regexp[i].value)
			}
		}
	}
	return res
}

// GetIdx is probe 1 alone (index pruning, plain allocations). The result is
// caller-owned exactly like Get.
func (s *ruleStore[T]) GetIdx(url string) []T {
	return s.getProbe(url, true, false)
}

// GetLP is probe 2 alone (low-allocation traversal + pooled result). The
// caller MUST call putRes on the returned slice exactly once.
func (s *ruleStore[T]) GetLP(url string) []T {
	return s.getProbe(url, false, true)
}

// GetIdxLP is probe 1 + probe 2 combined. The caller MUST call putRes on the
// returned slice exactly once.
func (s *ruleStore[T]) GetIdxLP(url string) []T {
	return s.getProbe(url, true, true)
}

func (s *ruleStore[T]) Compact() {
	s.generic = trimslice.TrimSlice(s.generic)
	s.regexp = trimslice.TrimSlice(s.regexp)
	s.tree.Compact()
}
