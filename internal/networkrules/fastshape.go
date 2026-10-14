package networkrules

import (
	"regexp"
	"regexp/syntax"
	"strings"
)

// Shape specialization for regexp rules (2026-10-05).
//
// For a restricted family of general pattern shapes this file builds a
// fastShape: a matcher that is EXACTLY equivalent to the compiled regexp.
// ruleStore uses it to keep the regexp engine off the per-request hot path.
// Any pattern outside the family keeps using its *regexp.Regexp (fallback).
// Equivalence has absolute priority over coverage: whenever a construct is
// not provably reproduced by the byte-oriented matcher below, the pattern
// falls back. All decisions are made on general format properties of the
// syntax tree; no pattern text, subscription or site is ever special-cased.
//
// Supported family: an optional ^http(s)?:// prefix (when the pattern starts
// with "^"; unanchored patterns apply to the whole URL instead), then items
// separated by free gaps (".*", a star of OpAnyChar/OpAnyCharNotNL):
//
//   - literal strings (ASCII bytes only);
//   - ASCII-only character classes, optionally repeated: [cls] [cls]{n,m}
//     [cls]+ [cls]* [cls]?, attached to the literal in front of them; a
//     single-character class folds into an ordinary literal;
//   - repetitions of literals ("(ab){2,}" == "abab" occurs), for the same
//     absorption reason;
//   - (capturing or not) groups and alternations of the above, which fork
//     the pending cross-product (e.g. `bit(ly)?\.(com|ly)` becomes four
//     literal alternatives; `bid|biz` style prefix factoring becomes one
//     alternative per branch; optional literal groups inside branches fork
//     into every spelling);
//   - a ".*\/" sequence, which constrains the remaining content to sit
//     after the first "/" of the URL (needPath);
//   - an optional trailing "$" (OpEndText), which anchors the last unit to
//     the end of the URL (a class run must then extend to the end with its
//     length inside [min, max] — there the maximum binds and is therefore
//     tracked).
//
// The compiled AST of such a pattern is a concatenation of the optional
// prefix followed by "required units" separated by free gaps. Because a free
// gap accepts any string, the pattern matches iff the units can be placed at
// non-decreasing positions, each unit matching one of its alternatives, and
// each unit's placement starting at-or-after the previous unit's consumed
// span (literal + minimum class run). Positional special cases:
//
//   - units[0].anchored: no free gap precedes the first unit, so it must
//     match exactly at the start of the scope (right after the scheme);
//   - units[last].atEnd: "$" directly after the unit's content (Go's
//     OpEndText matches only at the end of the text), so the last unit must
//     end exactly at the end of the scope;
//   - "$" after a free gap (or after a group able to match empty, when
//     position freedom makes that satisfiable) imposes no extra constraint.
//
// Deliberate limitations (all of them fall back to the regexp):
//
//   - Case folding: (?i) makes literals and classes match Unicode case
//     variants, including multi-byte runes (e.g. U+212A folds with 'k'),
//     which byte-wise scanning cannot reproduce.
//   - Non-ASCII literals and classes that can match non-ASCII runes (negated
//     classes such as \S, explicit high bytes): regexp matches runes while
//     this matcher scans bytes; the two disagree on invalid UTF-8 input and
//     on run counting, so they are not accepted.
//   - Newlines: an unflagged "." is OpAnyCharNotNL and does not match '\n',
//     while the gaps here are position-free. Patterns containing
//     OpAnyCharNotNL are flagged nlSensitive; for URLs containing '\n'
//     matchFast defers to the original regexp, which is kept in re for
//     exactly this purpose. URLs that reach this store in practice come
//     from parsed requests and cannot contain '\n', but the fallback keeps
//     the equivalence unconditional.
//   - Character classes followed by anything but a free gap or the pattern
//     end (e.g. `lit[0-9]{2}x`, "li[0-9]?t"): the class would sit between
//     literals and break the contiguity the model expresses. (For an end-
//     anchored unit the run must reach the end, which IS expressed — see
//     fastAlt.clsMax.)
//   - A ".*\/" sequence twice (two slashes would be required), or ".*\/"
//     followed by anything but a free gap: the model cannot express that
//     ordering.
//   - Lookarounds, word boundaries, plus/star/quest of groups, optional
//     class runs inside alternation branches, bounded optional literal
//     repetitions, and everything else not listed above.

// fastShape is an exact equivalent of one supported pattern. It is immutable
// after construction and safe for concurrent use.
type fastShape struct {
	re *regexp.Regexp // original compiled pattern; exact answer for '\n' URLs
	// units are the required units in pattern order.
	units []fastUnit
	// hasScheme marks patterns anchored with the ^http(s)?:// prefix; the
	// two flags below say which schemes that prefix accepts. Unanchored
	// patterns have hasScheme=false and match against the whole URL.
	hasScheme  bool
	allowHTTP  bool
	allowHTTPS bool
	needPath   bool // remaining units must sit after the first "/" of the scope
	// nlSensitive marks patterns containing OpAnyCharNotNL (an unflagged
	// "."): their gap semantics differ from this matcher on '\n' input.
	nlSensitive bool
	// endEmpty marks a pattern that is nothing but the prefix and "$": the
	// scope must be empty.
	endEmpty bool
}

// fastUnit is one required unit: the alternatives are OR-ed, the units are
// AND-ed in order.
type fastUnit struct {
	alts []fastAlt
	// anchored units must match exactly at the start of the search window
	// (only units[0] can be anchored).
	anchored bool
	// atEnd units must end at the end of the scope (only the last unit
	// can).
	atEnd bool
}

// fastAlt is one alternative of a unit: a required literal optionally
// followed by a run of at least clsMin (and, only for atEnd alternatives,
// at most clsMax) characters from cls. Away from the end of the pattern a
// free gap always follows a unit, so any longer run is absorbed by the gap
// and clsMax never binds; directly before "$" the run must extend to the
// end of the scope and its length is bounded, which is the only place where
// clsMax is used. cls with clsMin == 0 is an optional run: it imposes no
// constraint on its own, but it still marks the alternative so that a
// literal appended after it is rejected (the class would sit between
// literals, breaking contiguity).
type fastAlt struct {
	lit    string       // required literal ("" allowed for a bare class run)
	cls    *fastByteSet // required character class after lit (nil = none)
	clsMin int          // minimum run length (0 = optional run)
	clsMax int          // maximum run length; < 0 = unbounded (used only at atEnd)
}

// fastByteSet is a set of the 256 byte values.
type fastByteSet [4]uint64

func (s *fastByteSet) add(b byte)      { s[b/64] |= 1 << (b % 64) }
func (s *fastByteSet) has(b byte) bool { return s[b/64]&(1<<(b%64)) != 0 }

const (
	fastMaxPendEntries   = 64  // cap on the fork cross-product; larger => fallback
	fastMaxBranchAlts    = 16  // cap on one alternation branch's expansion
	fastMaxUnits         = 32  // cap on ordered required units; larger => fallback
	fastMaxLiteralRepeat = 128 // cap on "(ab){n}" literal expansion length
)

// pendAlt is a pending alternative: a literal chain (with optional literal
// forks folded in) optionally followed by one class run.
type pendAlt struct {
	lit    string
	cls    *fastByteSet
	clsMin int
	clsMax int
}

func (e pendAlt) plus(lit string) (pendAlt, bool) {
	if e.cls != nil {
		// A literal after a class run would sit between classes/literals;
		// the model does not express that contiguity.
		return pendAlt{}, false
	}
	e.lit += lit
	return e, true
}

func pendHasContent(pend []pendAlt) bool {
	for _, e := range pend {
		if e.lit != "" || e.cls != nil {
			return true
		}
	}
	return false
}

// pendAppendLit appends a literal to every pending alternative.
func pendAppendLit(pend []pendAlt, lit string) ([]pendAlt, bool) {
	for j := 0; j < len(lit); j++ {
		if lit[j] >= 0x80 {
			// Non-ASCII literal: regexp matches runes, this matcher scans
			// bytes; they disagree on invalid UTF-8 input.
			return nil, false
		}
	}
	for j := range pend {
		e, ok := pend[j].plus(lit)
		if !ok {
			return nil, false
		}
		pend[j] = e
	}
	return pend, true
}

// pendAttachClass attaches a class run to every pending alternative.
func pendAttachClass(pend []pendAlt, cls *fastByteSet, min, max int) ([]pendAlt, bool) {
	for j := range pend {
		if pend[j].cls != nil {
			// A second class on the same alternative ("[a-z][0-9]") is not
			// expressible.
			return nil, false
		}
		pend[j].cls = cls
		pend[j].clsMin = min
		pend[j].clsMax = max
	}
	return pend, true
}

// pendForkLiterals adds optional literals to the cross-product (quest-like):
// every pending alternative keeps its "skip" copy and gains one copy per
// literal with it appended.
func pendForkLiterals(pend []pendAlt, lits []string) ([]pendAlt, bool) {
	out := make([]pendAlt, 0, (1+len(lits))*len(pend))
	for _, e := range pend {
		out = append(out, e)
		for _, lit := range lits {
			ne, ok := e.plus(lit)
			if !ok {
				return nil, false
			}
			out = append(out, ne)
		}
	}
	if len(out) > fastMaxPendEntries {
		return nil, false
	}
	return out, true
}

// pendExpand folds one regexp item (or concatenation of items) into the
// pending alternatives, mirroring the top-level mechanics without free
// gaps. Used for alternation branches, where every item must chain onto the
// previous one contiguously.
func pendExpand(pend []pendAlt, r *syntax.Regexp) ([]pendAlt, bool) {
	switch r.Op {
	case syntax.OpLiteral:
		return pendAppendLit(pend, string(r.Rune))
	case syntax.OpEmptyMatch:
		return pend, true
	case syntax.OpCharClass:
		lit, cls, min, max, ok := classOf(r)
		if !ok {
			return nil, false
		}
		if lit != "" {
			return pendAppendLit(pend, lit)
		}
		return pendAttachClass(pend, cls, min, max)
	case syntax.OpConcat:
		for _, sub := range r.Sub {
			var ok bool
			pend, ok = pendExpand(pend, sub)
			if !ok {
				return nil, false
			}
		}
		return pend, true
	case syntax.OpCapture:
		if len(r.Sub) != 1 {
			return nil, false
		}
		return pendExpand(pend, r.Sub[0])
	case syntax.OpAlternate:
		// Fork: every branch expands a copy of the current list.
		var out []pendAlt
		for _, b := range r.Sub {
			branch := make([]pendAlt, len(pend))
			copy(branch, pend)
			branch, ok := pendExpand(branch, b)
			if !ok {
				return nil, false
			}
			if len(branch) > fastMaxBranchAlts {
				return nil, false
			}
			out = append(out, branch...)
		}
		if len(out) > fastMaxPendEntries {
			return nil, false
		}
		return out, true
	case syntax.OpQuest:
		// Optional literal group: fork into the skip copy and copies with
		// each literal spelling appended. Anything else inside an
		// alternation branch is not expressible.
		if len(r.Sub) != 1 {
			return nil, false
		}
		var lits []string
		switch inner := r.Sub[0]; {
		case inner.Op == syntax.OpLiteral:
			lits = []string{string(inner.Rune)}
		case inner.Op == syntax.OpCapture && len(inner.Sub) == 1 && inner.Sub[0].Op == syntax.OpLiteral:
			lits = []string{string(inner.Sub[0].Rune)}
		case inner.Op == syntax.OpCapture && len(inner.Sub) == 1 && inner.Sub[0].Op == syntax.OpAlternate:
			for _, b := range inner.Sub[0].Sub {
				if b.Op != syntax.OpLiteral {
					return nil, false
				}
				lits = append(lits, string(b.Rune))
			}
		default:
			return nil, false
		}
		return pendForkLiterals(pend, lits)
	case syntax.OpRepeat, syntax.OpPlus:
		if len(r.Sub) != 1 {
			return nil, false
		}
		min, max := 1, -1
		if r.Op == syntax.OpRepeat {
			min, max = r.Min, r.Max
			if min < 0 || (max >= 0 && max < min) {
				return nil, false
			}
		}
		switch tsub := r.Sub[0]; tsub.Op {
		case syntax.OpCharClass:
			_, cls, _, _, ok := classOf(tsub)
			if !ok {
				return nil, false
			}
			// min == 0 keeps the set as an optionality marker; its maximum
			// still binds at an end-anchored unit.
			return pendAttachClass(pend, cls, min, max)
		case syntax.OpLiteral:
			if min == 0 {
				// "(ab){0,3}" right before "$" would need a bounded
				// optional literal run: not expressible.
				return nil, false
			}
			lit := string(tsub.Rune)
			if len(lit)*min > fastMaxLiteralRepeat {
				return nil, false
			}
			return pendAppendLit(pend, strings.Repeat(lit, min))
		}
		return nil, false
	}
	return nil, false
}

// parseFastShape analyzes the pattern body of a compiled regexp rule and
// returns an exact fast matcher, or nil when the pattern is outside the
// supported family. re is the compiled regexp for the same body and is kept
// for inputs where the two could disagree (URLs containing '\n').
func parseFastShape(pattern string, re *regexp.Regexp) *fastShape {
	ast, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil
	}
	if !fastFlagsOK(ast) {
		return nil
	}

	switch ast.Op {
	case syntax.OpStar:
		// ".*" alone matches every input.
		if isAnyStar(ast) {
			return &fastShape{re: re}
		}
		return nil
	case syntax.OpAlternate:
		// Unanchored alternation of supported branches: one unit whose
		// alternatives are the branches.
		p := &fastParser{pend: []pendAlt{{}}}
		if !p.crossAlternates(ast.Sub) || !p.flush() {
			return nil
		}
		return p.finish(re)
	case syntax.OpConcat:
		// fall through to the generic walk below
	default:
		return nil
	}

	subs := ast.Sub
	p := &fastParser{pend: []pendAlt{{}}}
	i := 0
	if subs[0].Op == syntax.OpBeginText {
		p.anchored = true
		i = 1
		if !p.parseSchemePrefix(subs, &i) {
			return nil
		}
	} // else: unanchored — the whole URL is the scope, nothing is anchored

	for ; i < len(subs); i++ {
		if !p.parseItem(subs, &i) {
			return nil
		}
	}
	if !p.flush() {
		return nil
	}
	return p.finish(re)
}

// fastFlagsOK reports whether the tree avoids case folding anywhere (see the
// file comment for why FoldCase means fallback).
func fastFlagsOK(r *syntax.Regexp) bool {
	if r.Flags&syntax.FoldCase != 0 {
		return false
	}
	for _, sub := range r.Sub {
		if !fastFlagsOK(sub) {
			return false
		}
	}
	return true
}

func isExactLiteral(r *syntax.Regexp, s string) bool {
	return r.Op == syntax.OpLiteral && string(r.Rune) == s
}

// isOptionalLiteral reports whether r is an optional single literal, with or
// without a capture group: s? or (s?).
func isOptionalLiteral(r *syntax.Regexp, s string) bool {
	if r.Op != syntax.OpQuest || len(r.Sub) != 1 {
		return false
	}
	inner := r.Sub[0]
	if isExactLiteral(inner, s) {
		return true
	}
	return inner.Op == syntax.OpCapture && len(inner.Sub) == 1 && isExactLiteral(inner.Sub[0], s)
}

// isAnyStar reports whether r is ".*": a star of any-char (a free gap,
// matching any string).
func isAnyStar(r *syntax.Regexp) bool {
	return r.Op == syntax.OpStar && len(r.Sub) == 1 &&
		(r.Sub[0].Op == syntax.OpAnyChar || r.Sub[0].Op == syntax.OpAnyCharNotNL)
}

type fastParser struct {
	units      []fastUnit
	pend       []pendAlt
	anchored   bool // next flushed unit has no free gap before it (units[0] only)
	hasScheme  bool
	allowHTTP  bool
	allowHTTPS bool
	needPath   bool
	// awaitGap is set directly after ".*\/": the next item must be a free
	// gap (or the pattern end), otherwise the slash is not a free fork
	// point and the ordering cannot be reproduced.
	awaitGap    bool
	gapSeen     bool // a free gap (or needPath) occurred since the prefix
	lastAtEnd   bool // the pattern ends with "$"
	nlSensitive bool
	// lastFlushCreated records whether the most recent flush appended a
	// unit; endPendContent records whether anything was pending when "$"
	// was seen (the final flush runs afterwards).
	lastFlushCreated bool
	endPendContent   bool
}

func (p *fastParser) parseItem(subs []*syntax.Regexp, i *int) bool {
	r := subs[*i]
	// Directly after ".*\/" only a free gap may follow; e.g. `.*\/(x|y)`
	// requires the group right after the slash, which the model — whose
	// units are searched freely — cannot express.
	if p.awaitGap {
		if !isAnyStar(r) {
			return false
		}
		p.awaitGap = false
		// Fall through: the star is processed as a normal gap below.
	}

	switch r.Op {
	case syntax.OpStar:
		if isAnyStar(r) {
			if r.Sub[0].Op == syntax.OpAnyCharNotNL {
				p.nlSensitive = true
			}
			// ".*\/": the following content must sit after a slash. Note
			// that `\/foo` compiles to the single merged literal "/foo", so
			// this branch only fires for a lone slash; a merged literal is
			// handled as an ordinary literal, which is exact because the
			// star in front of it is a free gap ("contains /foo").
			if *i+1 < len(subs) && isExactLiteral(subs[*i+1], "/") {
				if p.needPath || len(p.units) > 0 || pendHasContent(p.pend) {
					// Content before the fork point would have to be
					// constrained to the pre-slash region, and a second
					// ".*\/" would require two slashes.
					return false
				}
				p.needPath = true
				p.awaitGap = true
				p.anchored = false
				p.gapSeen = true
				*i++ // consume the "/" literal
				return true
			}
			if !p.flush() {
				return false
			}
			p.anchored = false
			p.gapSeen = true
			return true
		}
		// Star of a single class: an optional run, no constraint away from
		// the end, but it marks the alternative (a literal after it is
		// rejected — see fastAlt.clsMin). The raw set is used even for a
		// single-character class: folding it into a required literal would
		// drop the optionality ("x[.]*y" also matches "xy").
		if len(r.Sub) == 1 {
			_, cls, _, _, ok := classOf(r.Sub[0])
			if !ok {
				return false
			}
			_, ok = pendAttachClass(p.pend, cls, 0, -1)
			return ok
		}
		return false

	case syntax.OpLiteral:
		_, ok := pendAppendLit(p.pend, string(r.Rune))
		return ok

	case syntax.OpCharClass:
		lit, cls, min, max, ok := classOf(r)
		if !ok {
			return false
		}
		if lit != "" {
			_, ok = pendAppendLit(p.pend, lit)
			return ok
		}
		_, ok = pendAttachClass(p.pend, cls, min, max)
		return ok

	case syntax.OpRepeat:
		// `lit[cls]{min,max}` (e.g. `\?{4,}`) or `lit(ab){2,}`: followed by
		// a free gap or the pattern end, a repetition of at least min is
		// exactly equivalent to min concatenated copies of the repeated
		// unit occurring as a substring (any longer run is absorbed by the
		// gap). The one exception is the end-anchored unit, where the run
		// must reach the end and clsMax binds — it is stored and enforced
		// by the matcher. Max < 0 means unbounded ("{2,}").
		if len(r.Sub) != 1 || r.Min < 0 || (r.Max >= 0 && r.Max < r.Min) {
			return false
		}
		switch sub := r.Sub[0]; sub.Op {
		case syntax.OpCharClass:
			_, cls, _, _, ok := classOf(sub)
			if !ok {
				return false
			}
			_, ok = pendAttachClass(p.pend, cls, r.Min, r.Max)
			return ok
		case syntax.OpLiteral:
			if r.Min == 0 {
				// "(ab){0,3}" right before "$" would need a bounded
				// optional literal run: not expressible.
				return false
			}
			lit := string(sub.Rune)
			if len(lit)*r.Min > fastMaxLiteralRepeat {
				return false
			}
			_, ok := pendAppendLit(p.pend, strings.Repeat(lit, r.Min))
			return ok
		}
		return false

	case syntax.OpPlus:
		// `lit[cls]+` is a run of at least 1; `lit(ab)+` is "ab" occurring
		// at least once (same absorption argument).
		if len(r.Sub) != 1 {
			return false
		}
		switch sub := r.Sub[0]; sub.Op {
		case syntax.OpCharClass:
			_, cls, _, _, ok := classOf(sub)
			if !ok {
				return false
			}
			_, ok = pendAttachClass(p.pend, cls, 1, -1)
			return ok
		case syntax.OpLiteral:
			_, ok := pendAppendLit(p.pend, string(sub.Rune))
			return ok
		}
		return false

	case syntax.OpQuest:
		if len(r.Sub) != 1 {
			return false
		}
		inner := r.Sub[0]
		switch inner.Op {
		case syntax.OpCharClass:
			// An optional single class is a no-op constraint, but only
			// while nothing is pending: after a literal it would sit
			// between literals ("li[0-9]?t") and break contiguity.
			_, cls, _, max, ok := classOf(inner)
			if !ok || pendHasContent(p.pend) {
				return false
			}
			_, ok = pendAttachClass(p.pend, cls, 0, max)
			return ok
		case syntax.OpLiteral:
			out, ok := pendForkLiterals(p.pend, []string{string(inner.Rune)})
			if !ok {
				return false
			}
			p.pend = out
			return true
		case syntax.OpCapture:
			if len(inner.Sub) != 1 {
				return false
			}
			deep := inner.Sub[0]
			switch deep.Op {
			case syntax.OpLiteral:
				out, ok := pendForkLiterals(p.pend, []string{string(deep.Rune)})
				if !ok {
					return false
				}
				p.pend = out
				return true
			case syntax.OpAlternate:
				// (a|b)? — all alternates must be plain literals; one fork
				// adds every optional literal in a single pass.
				lits := make([]string, 0, len(deep.Sub))
				for _, b := range deep.Sub {
					if b.Op != syntax.OpLiteral {
						return false
					}
					lits = append(lits, string(b.Rune))
				}
				out, ok := pendForkLiterals(p.pend, lits)
				if !ok {
					return false
				}
				p.pend = out
				return true
			}
			return false
		}
		return false

	case syntax.OpCapture:
		if len(r.Sub) != 1 {
			return false
		}
		inner := r.Sub[0]
		switch inner.Op {
		case syntax.OpLiteral:
			_, ok := pendAppendLit(p.pend, string(inner.Rune))
			return ok
		case syntax.OpEmptyMatch:
			return true
		case syntax.OpAlternate:
			return p.crossAlternates(inner.Sub)
		}
		return false

	case syntax.OpAlternate:
		// A non-capturing group `(?:a|b)` dissolves into a bare alternate;
		// captures do not affect matching, so it is handled identically.
		return p.crossAlternates(r.Sub)

	case syntax.OpEndText:
		// Go's "$" (OpEndText without (?m)) matches only at the end of the
		// text, so it is an exact end anchor here.
		if *i != len(subs)-1 {
			return false
		}
		p.lastAtEnd = true
		p.endPendContent = pendHasContent(p.pend)
		return true

	case syntax.OpEmptyMatch:
		return true

	default:
		return false
	}
}

// parseSchemePrefix consumes the fixed ^http(s)?:// prefix. Adjacent literal
// runes merge in the AST, so the scheme and the "://" separator (plus
// whatever literal follows it) can appear as one literal; every accepted
// spelling maps to an exact scheme set. "http://" is not a prefix of
// "https://...", so the order below is unambiguous.
func (p *fastParser) parseSchemePrefix(subs []*syntax.Regexp, i *int) bool {
	if *i >= len(subs) {
		return false
	}
	p.hasScheme = true
	if r := subs[*i]; r.Op == syntax.OpLiteral && strings.HasPrefix(string(r.Rune), "https://") {
		p.allowHTTPS = true
		return p.appendLiteralAfterScheme(string(r.Rune)[len("https://"):], i)
	} else if r.Op == syntax.OpLiteral && strings.HasPrefix(string(r.Rune), "http://") {
		p.allowHTTP = true
		return p.appendLiteralAfterScheme(string(r.Rune)[len("http://"):], i)
	} else if isExactLiteral(r, "http") {
		p.allowHTTP = true
		*i++
		if *i < len(subs) && isOptionalLiteral(subs[*i], "s") {
			p.allowHTTPS = true
			*i++
		}
		if *i >= len(subs) {
			return false
		}
		lit := string(subs[*i].Rune)
		if subs[*i].Op != syntax.OpLiteral || !strings.HasPrefix(lit, "://") {
			return false
		}
		return p.appendLiteralAfterScheme(lit[len("://"):], i)
	}
	return false
}

// appendLiteralAfterScheme seeds the pending alternatives with the literal
// content that the merged prefix literal contributed after "://".
func (p *fastParser) appendLiteralAfterScheme(rest string, i *int) bool {
	*i++
	if rest == "" {
		return true
	}
	_, ok := pendAppendLit(p.pend, rest)
	return ok
}

// crossAlternates replaces the pending list with the cross product of the
// current entries and the given mandatory alternates, each expanded by
// pendExpand (literals, forks from optional literal groups, at most one
// trailing class run).
func (p *fastParser) crossAlternates(alts []*syntax.Regexp) bool {
	out := make([]pendAlt, 0, len(p.pend)*len(alts))
	for _, e := range p.pend {
		for _, alt := range alts {
			more, ok := pendExpand([]pendAlt{e}, alt)
			if !ok {
				return false
			}
			out = append(out, more...)
		}
		if len(out) > fastMaxPendEntries {
			return false
		}
	}
	if len(out) == 0 {
		return false
	}
	p.pend = out
	return true
}

// flush turns the pending alternatives into the next required unit.
func (p *fastParser) flush() bool {
	p.lastFlushCreated = false
	pend := p.pend
	p.pend = []pendAlt{{}}
	var alts []fastAlt
	for _, e := range pend {
		if e.lit == "" && (e.cls == nil || e.clsMin == 0) {
			// An alternative that matches empty at any position makes the
			// whole unit a no-op constraint wherever the unit has position
			// freedom. The one exception — an end-anchored unit — is
			// handled by finish, which re-checks with position knowledge.
			return true
		}
		alts = append(alts, fastAlt{
			lit:    e.lit,
			cls:    e.cls,
			clsMin: e.clsMin,
			clsMax: e.clsMax,
		})
	}
	if len(alts) == 0 {
		return true
	}
	if len(p.units) >= fastMaxUnits {
		return false
	}
	p.units = append(p.units, fastUnit{alts: alts, anchored: p.anchored})
	p.lastFlushCreated = true
	return true
}

// finish validates the flushed units and applies the end anchor. The final
// flush has already run; lastFlushCreated/endPendContent say whether "$" is
// directly followed by (and anchored to) a flushed unit.
func (p *fastParser) finish(re *regexp.Regexp) *fastShape {
	endEmpty := false
	if p.lastAtEnd {
		switch {
		case p.endPendContent && p.lastFlushCreated:
			// "$" directly after the unit's content: anchor it to the end.
			p.units[len(p.units)-1].atEnd = true
		case p.endPendContent && p.anchored:
			// Pending content existed but the final flush dropped it as a
			// no-op (an optional group able to match empty) — with a fixed
			// start position its span would have to reach the end, which
			// the model cannot express.
			return nil
		case p.endPendContent:
			// Dropped no-op unit without a fixed position: its empty match
			// at the end is always possible, so no constraint remains.
		case p.gapSeen:
			// "$" after a free gap: the gap reaches the end on '\n'-free
			// URLs and '\n' input falls back via nlSensitive.
		case !p.hasScheme:
			// Unanchored: an empty match at the end is always possible.
		default:
			// "^https?://$" — nothing but the prefix and "$": the scope
			// must be empty.
			endEmpty = true
		}
	}
	return &fastShape{
		re:          re,
		units:       p.units,
		hasScheme:   p.hasScheme,
		allowHTTP:   p.allowHTTP,
		allowHTTPS:  p.allowHTTPS,
		needPath:    p.needPath,
		nlSensitive: p.nlSensitive,
		endEmpty:    endEmpty,
	}
}

// classOf classifies a character-class node. A class whose byte set holds
// exactly one byte is additionally reported as a single-character literal
// (exact: the class matches precisely that ASCII rune), which lets chains
// like `[.]wp[.]pl` fold into ordinary literals — callers that need the
// optionality marker keep using the set. Multi-byte-capable classes
// (negated classes, explicit high bytes) are rejected: regexp counts runes
// while this matcher counts bytes, and the two disagree on multi-byte
// input. min/max describe the run bounds of the class itself (1..1).
func classOf(r *syntax.Regexp) (lit string, cls *fastByteSet, min, max int, ok bool) {
	if r.Op != syntax.OpCharClass {
		return "", nil, 0, 0, false
	}
	set := &fastByteSet{}
	count := 0
	var single byte
	ru := r.Rune
	for i := 0; i+1 < len(ru); i += 2 {
		lo, hi := ru[i], ru[i+1]
		if lo < 0 || hi >= 0x80 {
			return "", nil, 0, 0, false
		}
		for c := lo; c <= hi; c++ {
			set.add(byte(c))
			single = byte(c)
			count++
		}
	}
	if count == 1 {
		return string(single), set, 1, 1, true
	}
	return "", set, 1, 1, true
}

// matchFast reports whether url matches the pattern. For every input it must
// return the same result as re.MatchString(url).
func (sh *fastShape) matchFast(url string) bool {
	var scope string
	if sh.hasScheme {
		switch {
		case sh.allowHTTPS && strings.HasPrefix(url, "https://"):
			scope = url[len("https://"):]
		case sh.allowHTTP && strings.HasPrefix(url, "http://"):
			scope = url[len("http://"):]
		default:
			return false
		}
	} else {
		scope = url
	}
	if sh.endEmpty {
		return len(scope) == 0
	}
	if sh.nlSensitive && strings.IndexByte(scope, '\n') >= 0 {
		// An unflagged "." cannot cross '\n'; defer to the exact regexp.
		return sh.re.MatchString(url)
	}
	pos := 0
	if sh.needPath {
		i := strings.IndexByte(scope, '/')
		if i < 0 {
			return false
		}
		// Everything must sit strictly after the first slash: content
		// after a later slash is also after the first one, so the first
		// slash is the weakest (and therefore exact) existential choice.
		// Searching from i+1 (not i) keeps alternatives that start with
		// "/" (from a second `\/` merged into the literal) from matching
		// at the slash itself.
		pos = i + 1
	}
	return sh.matchFrom(0, pos, scope)
}

// matchFrom places units[ui:] at positions >= pos. For every unanchored unit
// the alternative/occurrence with the smallest end position dominates: any
// feasible placement of the following units stays feasible when an earlier
// end is chosen, so a single greedy pass per unit is exact. Anchored units
// have no position freedom, but their alternatives may consume different
// lengths, so every matching alternative starts its own continuation.
func (sh *fastShape) matchFrom(ui, pos int, scope string) bool {
	for ; ui < len(sh.units); ui++ {
		u := &sh.units[ui]
		if !u.anchored {
			best := -1
			for ai := range u.alts {
				if end := altFindFrom(scope, pos, &u.alts[ai], u.atEnd); end >= 0 && (best < 0 || end < best) {
					best = end
				}
			}
			if best < 0 {
				return false
			}
			pos = best
			continue
		}
		if u.atEnd {
			// Anchored and at end: the alternative must span the whole
			// remaining scope.
			for ai := range u.alts {
				if altSpansToEnd(scope, pos, &u.alts[ai]) {
					return true
				}
			}
			return false
		}
		for ai := range u.alts {
			if end, ok := altMatchAt(scope, pos, &u.alts[ai]); ok && sh.matchFrom(ui+1, end, scope) {
				return true
			}
		}
		return false
	}
	return true
}

// altMatchAt reports whether alt matches exactly at pos (anchored case) and
// returns the end of its consumed span: literal plus the minimum class run —
// the run may extend further into the free gap that always follows a unit.
func altMatchAt(scope string, pos int, a *fastAlt) (int, bool) {
	if !strings.HasPrefix(scope[pos:], a.lit) {
		return 0, false
	}
	end := pos + len(a.lit)
	if a.cls != nil && a.clsMin > 0 {
		if classRunLen(scope, end, a.cls) < a.clsMin {
			return 0, false
		}
		end += a.clsMin
	}
	return end, true
}

// altSpansToEnd reports whether alt matches at pos and its consumed span
// reaches the end of the scope (anchored + "$").
func altSpansToEnd(scope string, pos int, a *fastAlt) bool {
	if !strings.HasPrefix(scope[pos:], a.lit) {
		return false
	}
	return classRunToEnd(scope, pos+len(a.lit), a.cls, a.clsMin, a.clsMax)
}

// altFindFrom returns the smallest end position >= pos where alt matches, or
// -1. For a fixed alternative only the earliest occurrence needs to be
// tried: a later occurrence only tightens the constraints of the units that
// follow. atEnd alternatives must end exactly at the end of the scope.
func altFindFrom(scope string, pos int, a *fastAlt, atEnd bool) int {
	if atEnd {
		return altFindEnd(scope, pos, a)
	}
	if len(a.lit) == 0 {
		// Bare class run (or an optional one, which is a no-op).
		if a.cls == nil || a.clsMin == 0 {
			return pos
		}
		return classRunFind(scope, pos, a.cls, a.clsMin)
	}
	from := pos
	for {
		j := strings.Index(scope[from:], a.lit)
		if j < 0 {
			return -1
		}
		p := from + j
		end := p + len(a.lit)
		if a.cls == nil || a.clsMin == 0 {
			return end
		}
		if classRunLen(scope, end, a.cls) >= a.clsMin {
			return end + a.clsMin
		}
		from = p + 1 // literal occurrences may overlap
	}
}

// altFindEnd returns len(scope) when the alternative matches somewhere at or
// after pos with its span ending exactly at the end of the scope, else -1.
// The class run after the literal must fill the remaining scope with its
// length inside [clsMin, clsMax] — there the maximum binds.
func altFindEnd(scope string, pos int, a *fastAlt) int {
	min, max := 0, 0
	if a.cls != nil {
		min, max = a.clsMin, a.clsMax
	}
	hi := len(scope) - len(a.lit) // last start position that leaves room for the run
	pMin := pos
	if max >= 0 {
		// run length R = len(scope) - p - len(lit) <= max
		if p := hi - max; p > pMin {
			pMin = p
		}
	}
	pMax := hi - min
	if pMin > pMax || pMax < 0 {
		return -1
	}
	if pMin < 0 {
		pMin = 0
	}
	for p := pMin; p <= pMax; {
		j := strings.Index(scope[p:pMax+len(a.lit)], a.lit)
		if j < 0 {
			return -1
		}
		p += j
		if a.cls == nil || classRunToEnd(scope, p+len(a.lit), a.cls, 0, -1) {
			return len(scope)
		}
		p++
	}
	return -1
}

// classRunToEnd reports whether the scope from from to its end consists
// entirely of class characters (all of them when cls != nil) and the run
// length lies within [min, max] (max < 0 = unbounded).
func classRunToEnd(scope string, from int, cls *fastByteSet, min, max int) bool {
	n := len(scope) - from
	if n < min {
		return false
	}
	if max >= 0 && n > max {
		return false
	}
	if cls == nil {
		return n == 0
	}
	for q := from; q < len(scope); q++ {
		if !cls.has(scope[q]) {
			return false
		}
	}
	return true
}

// classRunLen counts the class characters starting at from.
func classRunLen(scope string, from int, cls *fastByteSet) int {
	n := 0
	for from+n < len(scope) && cls.has(scope[from+n]) {
		n++
	}
	return n
}

// classRunFind returns the earliest end of a run of at least min class
// characters starting at or after pos, or -1.
func classRunFind(scope string, pos int, cls *fastByteSet, min int) int {
	run := 0
	for p := pos; p < len(scope); p++ {
		if cls.has(scope[p]) {
			run++
			if run >= min {
				return p + 1
			}
		} else {
			run = 0
		}
	}
	return -1
}
