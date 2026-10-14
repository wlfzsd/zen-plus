package nrprobe

// PROBE 3: fastshape.go scrutiny (tmp_enginebench only, 2026-10-05).
// Questions:
//   1. Which shape families do the real-corpus fast rules actually use?
//   2. Is matchFast allocation-free (AllocsPerRun == 0)?
//   3. Engine-level: what does fastshape still buy now that probe 1's token
//      index prunes the fallback regexps? (A/B: BenchmarkProbeModifyReqReal
//      _Idx vs _IdxNoFast / _All.)

import (
	"fmt"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

// fastShapeStats returns the family summary of one fast shape.
func fastShapeStats(sh *fastShape) (units, alts int, scheme, needPath, atEnd, nl bool) {
	units = len(sh.units)
	for _, u := range sh.units {
		alts += len(u.alts)
	}
	return units, alts, sh.hasScheme, sh.needPath, units > 0 && sh.units[units-1].atEnd, sh.nlSensitive
}

// censusFastShapes dumps every fast-shape rule of one store.
func censusFastShapes[T comparable](label string, st *ruleStore[T], n *int) {
	for i := range st.regexp {
		r := &st.regexp[i]
		if r.fast == nil {
			continue
		}
		*n++
		units, alts, scheme, needPath, atEnd, nl := fastShapeStats(r.fast)
		fmt.Printf("%s fast#%02d units=%d alts=%d scheme=%v needPath=%v atEnd=%v nl=%v pat=%.85s\n",
			label, *n, units, alts, scheme, needPath, atEnd, nl, r.regexp.String())
	}
}

// TestFastShapeCensus dumps the family profile of every fast-shape rule in
// the real engine.
func TestFastShapeCensus(t *testing.T) {
	nr := probeBuildReal(t)
	var n int
	censusFastShapes("PRIMARY", nr.primaryStore, &n)
	censusFastShapes("EXCEPT ", nr.exceptionStore, &n)
	t.Logf("fast rules total: %d", n)
}

// zeroAllocScan checks matchFast allocation behavior across one store.
func zeroAllocScan[T comparable](st *ruleStore[T], urls []string, maxAllocs *int, checked *int, t *testing.T) {
	for i := range st.regexp {
		r := &st.regexp[i]
		if r.fast == nil {
			continue
		}
		*checked++
		for _, u := range urls {
			sh := r.fast
			a := int(testing.AllocsPerRun(20, func() {
				sh.matchFast(u)
			}))
			if a > *maxAllocs {
				*maxAllocs = a
				t.Logf("allocating matchFast: pat=%q url=%.60s allocs=%d", r.regexp.String(), u, a)
			}
		}
	}
}

// TestFastShapeZeroAlloc verifies matchFast performs zero allocations.
func TestFastShapeZeroAlloc(t *testing.T) {
	nr := probeBuildReal(t)
	urls := probeURLs(t, 200)
	maxAllocs, checked := 0, 0
	zeroAllocScan(nr.primaryStore, urls, &maxAllocs, &checked, t)
	zeroAllocScan(nr.exceptionStore, urls, &maxAllocs, &checked, t)
	if maxAllocs != 0 {
		t.Errorf("matchFast allocates: max %d allocs/run", maxAllocs)
	} else {
		t.Logf("matchFast: 0 allocs/run across %d fast rules x %d urls", checked, len(urls))
	}
}

var _ = rule.Rule{}
