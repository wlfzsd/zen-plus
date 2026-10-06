package nrprobe

// PROBE (tmp_enginebench only, 2026-10-05): rarest-required-token reverse
// index for the regexp store — the Ghostery adblocker "most discriminative
// token" trick (packages/adblocker/src/engine/reverse-index.ts) applied to
// the ~404 patterns that fall back to *regexp.Regexp.
//
// Soundness contract (zero false negatives):
//   - requiredSegments(pattern) returns contiguous byte strings that EVERY
//     accepting match must contain. It only descends through constructs that
//     certainly participate in every match (concatenation, capture groups,
//     >=1 repetitions of pure literals); alternations, optional/repeated
//     groups, case folding and non-ASCII literals all stop it (conservative:
//     fewer tokens, never a wrong token).
//   - A rule is indexed under its segment with the lowest pattern-corpus
//     document frequency (ties: longer, then earlier) — the "rarest token".
//   - Lookup marks candidate rules via the first min(len,4) bytes of the
//     token (a fixed k-gram bucket map over every window of the URL), then
//     confirms with strings.Contains before running the regexp. A k-gram hit
//     is necessary-but-not-sufficient; the Contains check plus the original
//     match decision keep the result bit-identical.
//   - Rules without any provable token (tok == nil) are always evaluated.
//   - Result ORDER is preserved: GetIdx walks the regexp slice in insertion
//     order and only SKIPS provably-impossible rules, so the appended values
//     are in exactly the same order as ruleStore.Get.
//
// This file is probe code: production integration would fold GetIdx into
// ruleStore.Get behind the index being non-nil.

import (
	"regexp/syntax"
	"strings"
)

// reIdx is the reverse index over one ruleStore's regexp slice.
// Immutable after build; safe for concurrent GetIdx.
type reIdx struct {
	// tokenOf[i] is the required literal for regexp[i], or "" when the rule
	// must always be evaluated (no provable token, or a fast-shape rule that
	// never touches the regexp engine on '\n'-free URLs).
	// Each entry is an "any-of" set: a rule is a candidate iff at least one
	// of its tokens occurs in the URL (a single required token is the size-1
	// case; a mandatory alternation contributes one token per branch).
	tokensOf [][]string
	// buckets[k] maps the k-byte window (k=2,3,4) of a token to the rule
	// indices indexed under tokens whose first k bytes equal the window.
	buckets [5]map[uint32][]int32
}

const (
	idxMinTokenLen = 2   // shorter tokens cannot prune (skip single bytes)
	idxMaxTokenLen = 128 // sanity cap; longer segments are useless anyway
	// idxMaxRules is the candidate-bitmap capacity (64*64 bits); stores with
	// more regexp rules fall back to the unindexed scan in GetIdx.
	idxMaxRules = 64 * 64
	// maxFastMaxLiteralRepeat mirrors fastMaxLiteralRepeat for repeat expansion.
	idxMaxLiteralRepeat = 128
)

// requiredSegments returns the contiguous byte strings that every match of
// pattern must contain. Empty slice = no provable token.
//
// Segments forced to sit at a fixed offset from position 0 (the leading
// literal run of an ^-anchored pattern, before any variable-length item) are
// flagged locked: for URLs they are near-universally present (every URL
// starts with a scheme), so they prune nothing. Selection prefers unlocked
// candidates (see buildIdx).
func requiredSegments(pattern string) (segs []string, locked []bool) {
	ast, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil || hasFoldCase(ast) {
		return nil, nil
	}
	return segmentsOfNode(ast)
}

// segmentsOfNode is the node-level walk behind requiredSegments.
func segmentsOfNode(r *syntax.Regexp) (segs []string, locked []bool) {
	var walk func(r *syntax.Regexp, atFixedStart bool)
	walk = func(r *syntax.Regexp, atFixedStart bool) {
		if b, ok := literalBytes(r); ok {
			if b != "" {
				segs = append(segs, b)
				locked = append(locked, atFixedStart)
			}
			return
		}
		switch r.Op {
		case syntax.OpConcat:
			// The whole concat is not one literal run; per-child segments
			// remain required (contiguity between them is lost, which is
			// conservative — a shorter token is still required).
			for _, c := range r.Sub {
				walk(c, atFixedStart)
				// Once a child is not a pure literal producer, later
				// children no longer sit at a fixed offset from 0. Note
				// zero-width literal-only children (anchors) don't lift the
				// fixed-start status, which the recursive call preserves.
				if _, ok := literalBytes(c); !ok {
					atFixedStart = false
				}
			}
		case syntax.OpCapture:
			// A capture in a concat always participates in the match, so
			// its inner required literals are still required.
			if len(r.Sub) == 1 {
				walk(r.Sub[0], atFixedStart)
			}
		}
	}
	if r.Op == syntax.OpConcat && len(r.Sub) > 0 && r.Sub[0].Op == syntax.OpBeginText {
		// `^...`: content before the first gap is position-locked. The
		// BeginText child itself is zero-width literal-only, so the walk
		// keeps atFixedStart=true through it.
		walk(r, true)
	} else {
		walk(r, false)
	}
	return segs, locked
}

// altTokens covers patterns whose only provable requirement is a mandatory
// alternation (e.g. `(site1|site2|...)\.[a-z]{3}\/...`): every match goes
// through ONE branch, so "at least one branch token occurs" is a necessary
// condition. It returns the token set of the first mandatory alternate group
// (a root alternate, or an alternate nested in a mandatory concat child).
// nil when no such group is fully covered. Ghostery's reverse index uses
// the same per-branch token union.
//
// Note: regexp/syntax FACTORS common literal prefixes of alternation
// branches (kimcartoon|kiss-anime parses to "ki"+("mcartoon"|"ss-anime")),
// so branches are reconstructed recursively — see branchOneOf.
func altTokens(ast *syntax.Regexp) []string {
	var group *syntax.Regexp
	switch ast.Op {
	case syntax.OpAlternate:
		group = ast
	case syntax.OpConcat:
		// Optional siblings impose nothing; the first MANDATORY alternate
		// group yields a sound one-of set ("some branch of it must match").
		for _, c := range ast.Sub {
			if isMandatory(c) {
				if g := altGroupOf(c); g != nil {
					group = g
					break
				}
			}
		}
	default:
		return nil
	}
	if group == nil {
		return nil
	}
	var out []string
	for _, b := range group.Sub {
		ts, ok := branchOneOf(b)
		if !ok {
			return nil // some branch yields nothing: the set is not provable
		}
		out = append(out, ts...)
		if len(out) > idxMaxAltTokens {
			return nil
		}
	}
	return out
}

const idxMaxAltTokens = 64

// branchOneOf returns tokens that every match through branch b must contain
// (b is one branch of an alternate, so b's content is fully required). The
// result is usually a single required segment, or several when the parser
// factored the branch into prefix + nested alternate.
func branchOneOf(b *syntax.Regexp) ([]string, bool) {
	// Parser-factored branch first: literals around exactly one nested
	// alternate reconstruct longer, more selective tokens (e.g. "ki"+
	// ("mcartoon"|"ss-anime") → kimcartoon | kiss-anime) than the bare
	// prefix segment the plain walk would find.
	if b.Op == syntax.OpConcat {
		if out, ok := factoredOneOf(b); ok {
			return out, true
		}
	}
	if segs, _ := segmentsOfNode(b); len(segs) > 0 {
		best := ""
		for _, sg := range segs {
			if len(sg) >= idxMinTokenLen && len(sg) <= idxMaxTokenLen && len(sg) > len(best) {
				best = sg
			}
		}
		if best != "" {
			return []string{best}, true
		}
		// Segments exist but are all below the useful minimum; the factored
		// path above may still reconstruct longer ones.
	}
	return nil, false
}

// factoredOneOf handles a parser-factored branch: literals around exactly
// one nested alternate. Tokens are prefix+alternative+suffix — contiguous in
// every match, because only mandatory literal producers may sit next to the
// group (an optional gap in between destroys contiguity and aborts).
func factoredOneOf(b *syntax.Regexp) ([]string, bool) {
	altIdx := -1
	for i, c := range b.Sub {
		if altGroupOf(c) != nil {
			if altIdx >= 0 {
				return nil, false // two nested groups: give up
			}
			altIdx = i
		}
	}
	if altIdx < 0 {
		return nil, false
	}
	prefix, ok := literalRun(b.Sub[:altIdx])
	if !ok {
		return nil, false
	}
	suffix, ok := literalRun(b.Sub[altIdx+1:])
	if !ok {
		return nil, false
	}
	sub, ok := altGroupOneOf(altGroupOf(b.Sub[altIdx]))
	if !ok {
		return nil, false
	}
	var out []string
	for _, t := range sub {
		tok := prefix + t + suffix
		if len(tok) >= idxMinTokenLen && len(tok) <= idxMaxTokenLen {
			out = append(out, tok)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// literalRun concatenates the literal contributions of consecutive concat
// children. Optional/non-literal children break the run on both sides (an
// optional gap between two literals would destroy the contiguity a merged
// token claims), so it reports ok=false there.
func literalRun(children []*syntax.Regexp) (string, bool) {
	var sb strings.Builder
	for _, c := range children {
		p, ok := literalBytes(c)
		if !ok {
			return "", false
		}
		sb.WriteString(p)
	}
	return sb.String(), true
}

// altGroupOneOf is branchOneOf applied to every sub-branch of a nested
// alternate: every match through the group goes through one sub-branch, so
// the union of their tokens is a sound one-of set.
func altGroupOneOf(group *syntax.Regexp) ([]string, bool) {
	var out []string
	for _, b := range group.Sub {
		ts, ok := branchOneOf(b)
		if !ok {
			return nil, false
		}
		out = append(out, ts...)
		if len(out) > idxMaxAltTokens {
			return nil, false
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// altGroupOf unwraps a mandatory alternate group (bare or captured).
func altGroupOf(c *syntax.Regexp) *syntax.Regexp {
	switch {
	case c.Op == syntax.OpAlternate:
		return c
	case c.Op == syntax.OpCapture && len(c.Sub) == 1 && c.Sub[0].Op == syntax.OpAlternate:
		return c.Sub[0]
	}
	return nil
}

// isMandatory reports whether a concat child participates in every match
// (it is not optional or zero-min repeated).
func isMandatory(r *syntax.Regexp) bool {
	switch r.Op {
	case syntax.OpStar, syntax.OpQuest, syntax.OpEmptyMatch:
		return false
	case syntax.OpRepeat:
		return r.Min >= 1
	}
	return true
}

// literalBytes returns the byte string r always contributes contiguously
// (ok=true), or ok=false when r is not a pure literal producer.
func literalBytes(r *syntax.Regexp) (string, bool) {
	switch r.Op {
	case syntax.OpLiteral:
		for _, c := range r.Rune {
			if c >= 0x80 {
				// regexp matches runes; byte scanning disagrees on
				// invalid UTF-8 — same reasoning as fastshape.go.
				return "", false
			}
		}
		return string(r.Rune), true
	case syntax.OpEmptyMatch, syntax.OpBeginText, syntax.OpEndText:
		return "", true // zero-width: contributes nothing, breaks nothing
	case syntax.OpCapture:
		if len(r.Sub) == 1 {
			return literalBytes(r.Sub[0])
		}
		return "", false
	case syntax.OpCharClass:
		// A class holding exactly one ASCII rune matches precisely that
		// byte (e.g. `[.]`); same folding argument as fastshape.classOf.
		if len(r.Rune) == 2 && r.Rune[0] == r.Rune[1] && r.Rune[0] < 0x80 {
			return string(byte(r.Rune[0])), true
		}
		return "", false
	case syntax.OpRepeat:
		// {min,} of a pure literal with min >= 1 forces min adjacent copies.
		if len(r.Sub) == 1 && r.Min >= 1 {
			if b, ok := literalBytes(r.Sub[0]); ok && len(b)*r.Min <= idxMaxLiteralRepeat {
				return strings.Repeat(b, r.Min), true
			}
		}
		return "", false
	case syntax.OpPlus:
		if len(r.Sub) == 1 {
			if b, ok := literalBytes(r.Sub[0]); ok && len(b) <= idxMaxLiteralRepeat {
				return b, true
			}
		}
		return "", false
	}
	return "", false
}

// hasFoldCase mirrors fastshape.fastFlagsOK: any FoldCase anywhere means no
// byte-literal can be proven.
func hasFoldCase(r *syntax.Regexp) bool {
	if r.Flags&syntax.FoldCase != 0 {
		return true
	}
	for _, sub := range r.Sub {
		if hasFoldCase(sub) {
			return true
		}
	}
	return false
}

// buildIdx builds the reverse index for the store's regexp slice.
// Insert must not run concurrently (same contract as ruleStore.Get).
func (s *ruleStore[T]) buildIdx() *reIdx {
	idx := &reIdx{tokensOf: make([][]string, len(s.regexp))}

	// 1) candidate tokens per fallback rule (fast-shape rules keep nil):
	//    case A — one globally required segment (any-of set of size 1);
	//    case B — a mandatory alternation: one required segment per branch
	//    (any-of set; a match goes through some branch, so its segment must
	//    occur). All candidate strings join the df universe.
	cands := make([][]string, len(s.regexp))
	locks := make([][]bool, len(s.regexp))
	altSets := make([][]string, len(s.regexp))
	var distinct []string
	seen := make(map[string]struct{})
	addDistinct := func(sg string) {
		if _, ok := seen[sg]; !ok {
			seen[sg] = struct{}{}
			distinct = append(distinct, sg)
		}
	}
	for i := range s.regexp {
		if s.regexp[i].fast != nil {
			continue
		}
		ast, err := syntax.Parse(s.regexp[i].regexp.String(), syntax.Perl)
		if err != nil || hasFoldCase(ast) {
			continue
		}
		segs, lks := segmentsOfNode(ast)
		var kept []string
		var keptLocked []bool
		for j, sg := range segs {
			if len(sg) < idxMinTokenLen || len(sg) > idxMaxTokenLen {
				continue
			}
			kept = append(kept, sg)
			keptLocked = append(keptLocked, lks[j])
			addDistinct(sg)
		}
		// Collect candidates from both cases; the selection between them
		// happens after df is known (pass 3).
		if len(kept) > 0 {
			cands[i] = kept
			locks[i] = keptLocked
		}
		if set := altTokens(ast); len(set) > 0 {
			altSets[i] = set
			for _, sg := range set {
				addDistinct(sg)
			}
		}
	}

	// 2) document frequency with substring containment: df[t] = number of
	// rules that provably require a superstring of t. This makes generic
	// tokens ("http://", "www.") lose against rare ones. Insert-time only.
	df := make(map[string]int32, len(distinct))
	for _, segs := range cands {
		if len(segs) == 0 {
			continue
		}
		hit := make(map[string]struct{}, len(distinct))
		for _, sg := range segs {
			for _, t := range distinct {
				if _, done := hit[t]; done {
					continue
				}
				if len(t) <= len(sg) && strings.Contains(sg, t) {
					df[t]++
					hit[t] = struct{}{}
				}
			}
		}
	}

	// 3) pick tokens and fill the buckets. Case A: rarest segment (unlocked
	// preferred, then lower df, then longer). Case B: per-branch rarest.
	for k := 2; k <= 4; k++ {
		idx.buckets[k] = make(map[uint32][]int32)
	}
	addToken := func(rule int32, tok string) {
		k := len(tok)
		if k > 4 {
			k = 4
		}
		key := kgramOf(tok, k)
		idx.buckets[k][key] = append(idx.buckets[k][key], rule) // #nosec G115 -- slice index
	}
	for i := range cands {
		if len(cands[i]) > 0 {
			best, bestLocked := "", false
			bestDF := int32(-1)
			for j, sg := range cands[i] {
				d := df[sg]
				lk := locks[i][j]
				if bestDF < 0 ||
					(bestLocked && !lk) ||
					(lk == bestLocked && (d < bestDF || (d == bestDF && len(sg) > len(best)))) {
					best, bestLocked, bestDF = sg, lk, d
				}
			}
			// Case A normally wins. Exception: when the only caseA token is
			// LOCKED (a fixed-offset scheme-prefix segment that occurs in
			// every URL and prunes nothing) and a caseB one-of set exists,
			// the alternation tokens are the better discriminator.
			if best != "" && !(bestLocked && len(altSets[i]) > 0) {
				idx.tokensOf[i] = []string{best}
				addToken(int32(i), best) // #nosec G115 -- slice index
				continue
			}
		}
		brs := altSets[i]
		if len(brs) == 0 {
			continue
		}
		setSeen := make(map[string]struct{}, len(brs))
		set := make([]string, 0, len(brs))
		for _, tok := range brs {
			if _, dup := setSeen[tok]; dup {
				continue
			}
			setSeen[tok] = struct{}{}
			set = append(set, tok)
		}
		if len(set) == 0 {
			continue
		}
		idx.tokensOf[i] = set
		for _, tok := range set {
			addToken(int32(i), tok) // #nosec G115 -- slice index
		}
	}
	return idx
}

// kgramOf packs the first k bytes of s.
func kgramOf(s string, k int) uint32 {
	switch k {
	case 4:
		return uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3])
	case 3:
		return uint32(s[0])<<16 | uint32(s[1])<<8 | uint32(s[2])
	default:
		return uint32(s[0])<<8 | uint32(s[1])
	}
}

// hitBits is the per-Get candidate bitmap (4096 rules max, stack allocated).
type hitBits [64]uint64

// markCandidates sets a bit per rule whose token k-gram occurs in url.
// Callers size it to 4096 rules and fall back to a full scan beyond that
// (see GetIdx).
func (idx *reIdx) markCandidates(url string, hit *hitBits) {
	for k := 4; k >= 2; k-- {
		m := idx.buckets[k]
		if len(m) == 0 {
			continue
		}
		for i := 0; i+k <= len(url); i++ {
			ids := m[kgramOf(url[i:], k)]
			for _, id := range ids {
				hit[id/64] |= 1 << (id % 64)
			}
		}
	}
}
