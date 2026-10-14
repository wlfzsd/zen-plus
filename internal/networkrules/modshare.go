package networkrules

// Modifier-block sharing (2026-10-13, batch 3 — borrowed from upstream
// #821 and adapted to this fork's two-group Rule shape):
//
// Filter lists repeat modifier text heavily — AdGuard Spyware alone ships
// 200k+ rules of the form "…$image". Parsing each rule allocates fresh
// condition/action/query modifier objects per rule; rules with identical
// modifier text always parse into identical objects (ParseModifiers is a
// deterministic function of the modifier text — including the
// linkDomainToTarget pass at rule.go:366-372, which is itself text-
// determined). So after parsing, a rule can safely swap in the parsed
// modifier groups of a canonical rule with the same text, discarding its
// own duplicate: the groups are immutable after ParseModifiers returns
// (every mutation site — ensureCondMods/ensureActMods appends and the
// MatchTargetDomain link — lives inside ParseModifiers, rule.go:337-376).
//
// Differences from upstream #821: this fork already stores modifier slices
// behind two lazy pointers (ruleCondMods/ruleActMods, 2026-10-05), so no
// Rule-struct reshuffle is needed — sharing swaps those two pointers via
// rule.Rule.ShareModsFrom and keeps every flag (Document/Popup/Important/
// All/Badfilter/FrameScoped) as a per-rule field, set inside the rule's
// own ParseModifiers run. Exception rules share too: their embedded
// rule.Rule goes through the same hook.
//
// The interner is dropped at Compact (upstream #821 semantics): live
// allowlist rules parsed afterwards stay unshared and cannot pin the map.

import (
	"strings"
	"sync"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

type modsInterner struct {
	mu sync.Mutex
	m  map[string]*rule.Rule
}

func newModsInterner() *modsInterner {
	return &modsInterner{m: make(map[string]*rule.Rule)}
}

// shareMods swaps r's parsed modifier groups for the canonical rule's when
// a rule with the same modifier text was parsed before; otherwise r
// becomes the canonical rule for that text. modifiers is the split
// modifier list; joining it back with ',' reproduces the raw text because
// parseRuleParts splits on unescaped commas only.
func (nr *NetworkRules) shareMods(r *rule.Rule, modifiers []string) {
	if nr.modsInterner == nil || len(modifiers) == 0 {
		return
	}
	key := strings.Join(modifiers, ",")
	nr.modsInterner.mu.Lock()
	defer nr.modsInterner.mu.Unlock()
	if nr.modsInterner.m == nil {
		return // dropped at Compact
	}
	if canon, ok := nr.modsInterner.m[key]; ok {
		if canon != r {
			r.ShareModsFrom(canon)
		}
		return
	}
	nr.modsInterner.m[key] = r
}

// dropModsInterner releases the map at Compact. Rules already share the
// canonical blocks through their own pointers; live rules parsed after
// this simply stay unshared.
func (nr *NetworkRules) dropModsInterner() {
	if nr.modsInterner == nil {
		return
	}
	nr.modsInterner.mu.Lock()
	nr.modsInterner.m = nil
	nr.modsInterner.mu.Unlock()
}

// size reports the number of interned modifier texts (test/observability).
func (mi *modsInterner) size() int {
	if mi == nil {
		return 0
	}
	mi.mu.Lock()
	defer mi.mu.Unlock()
	return len(mi.m)
}
