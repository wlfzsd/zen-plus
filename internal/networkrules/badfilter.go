package networkrules

// $badfilter post-load disablement (2026-10-08 B6).
//
// Semantics per AdGuard doc #badfilter-modifier (lines 1474-1515):
//   - A $badfilter rule disables the basic rule whose text equals the
//     badfilter rule's text with the badfilter modifier removed, exactly
//     (doc 1476-1483). This covers hosts-derived rules (their synthesized
//     RawRule "||host^$document", parse.go) and exception rules (doc 1482).
//   - A badfilter rule with a $domain modifier without negated entries
//     additionally disables the corresponding rule only for the domains
//     specified in both the badfilter and the basic rule (doc 1485-1499,
//     intersection semantics, including wildcard-TLD entries like
//     "example.*").
//
// Implementation: rules carrying $badfilter are collected at parse time and
// never inserted into the stores; applyBadfilters runs once from Compact —
// after every filter list was fully loaded (a badfilter rule may precede or
// follow its target in load order, 禁逐条即时禁用). Targets are found by a
// one-shot walk over both stores (ruleStore.walkValues) and disabled through
// a pointer flag instead of a tree removal.

import (
	"strings"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rulemodifiers"
)

// badfilterRule is one collected $badfilter rule.
type badfilterRule struct {
	// text is the rule text with the badfilter modifier removed. A stored
	// rule whose RawRule equals text is disabled outright.
	text string
	// normText is text with the $domain modifier removed: the
	// correspondence key for domain-level partial disablement (doc 1494:
	// "/some$domain=example.com,badfilter" disables
	// "/some$domain=example.com|example.org|example.io" for example.com).
	normText string
	// domains is the parsed $domain value of the badfilter rule when it has
	// one without negated entries (doc 1487-1488); nil otherwise.
	domains *rulemodifiers.DomainModifier
}

// addBadfilter registers a $badfilter rule for post-load application in
// Compact. pattern and modifiers come from parseRuleParts; the badfilter
// modifier itself is dropped from the kept text.
func (nr *NetworkRules) addBadfilter(pattern string, modifiers []string) {
	kept := make([]string, 0, len(modifiers))
	domainValue := ""
	hasDomain := false
	for _, m := range modifiers {
		if m == "badfilter" {
			continue
		}
		if name, _, hasValue := strings.Cut(m, "="); name == "domain" && hasValue {
			hasDomain = true
			domainValue = m[len("domain="):]
		}
		kept = append(kept, m)
	}

	text := pattern
	if len(kept) > 0 {
		text = pattern + "$" + strings.Join(kept, ",")
	}

	bf := badfilterRule{
		text:     text,
		normText: textWithoutDomain(text),
	}
	if hasDomain {
		dm := &rulemodifiers.DomainModifier{}
		// A negated $domain value (doc 1499) or an unparseable one opts
		// out of partial disablement; the exact-text match still applies.
		if err := dm.Parse("domain=" + domainValue); err == nil && !dm.Inverted() {
			bf.domains = dm
		}
	}

	nr.badfilters = append(nr.badfilters, bf)
}

// textWithoutDomain strips the $domain modifier from a rule text so that
// badfilter rules and their targets can be correlated regardless of the
// domain list each carries (doc 1494-1499). Both sides normalize the same
// way, so equality of the normalized texts is the correspondence test.
func textWithoutDomain(text string) string {
	pattern, mods := parseRuleParts(text)
	kept := make([]string, 0, len(mods))
	for _, m := range mods {
		if name, _, _ := strings.Cut(m, "="); name == "domain" {
			continue
		}
		kept = append(kept, m)
	}
	if len(kept) == 0 {
		return pattern
	}
	return pattern + "$" + strings.Join(kept, ",")
}

// applyBadfilters disables the rules targeted by the collected $badfilter
// rules. It must run only after every filter list was loaded (Compact).
func (nr *NetworkRules) applyBadfilters() {
	if len(nr.badfilters) == 0 {
		return
	}

	full := make(map[string]struct{}, len(nr.badfilters))
	partial := make([]badfilterRule, 0, len(nr.badfilters))
	for _, bf := range nr.badfilters {
		full[bf.text] = struct{}{}
		if bf.domains != nil {
			partial = append(partial, bf)
		}
	}

	apply := func(rawRule string, r *rule.Rule) {
		if _, ok := full[rawRule]; ok {
			r.ApplyBadfilterDisable(true, nil)
			return
		}
		if len(partial) == 0 {
			return
		}
		norm := textWithoutDomain(rawRule)
		for i := range partial {
			if norm == partial[i].normText {
				r.ApplyBadfilterDisable(false, partial[i].domains)
			}
		}
	}

	nr.primaryStore.walkValues(func(r *rule.Rule) {
		apply(r.RawRule, r)
	})
	nr.exceptionStore.walkValues(func(er *exceptionrule.ExceptionRule) {
		apply(er.RawRule, &er.Rule)
	})
}

// BadfilterAudit is a read-only diagnostic report of the post-load $badfilter
// effect (2026-10-08 B6). It is meant for filter-list audits and probes, not
// for the matching hot path.
type BadfilterAudit struct {
	// Collected is the number of $badfilter rules registered at parse time.
	Collected int
	// FullDisabled lists the RawRules disabled outright by an exact text
	// match with a $badfilter target.
	FullDisabled []string
	// PartialDisabled counts rules disabled only for specific referrer
	// domains by a $domain-bearing $badfilter rule.
	PartialDisabled int
	// PartialDisabledRaw lists the RawRules of the partially disabled
	// rules (one entry per stored rule instance, may repeat across lists).
	PartialDisabledRaw []string
}

// BadfilterAudit walks both stores once and reports the $badfilter
// disablement state. See BadfilterAudit.
func (nr *NetworkRules) BadfilterAudit() BadfilterAudit {
	audit := BadfilterAudit{Collected: len(nr.badfilters)}
	visit := func(rawRule string, bd *rule.BadfilterDisable) {
		if bd == nil {
			return
		}
		if bd.Full {
			audit.FullDisabled = append(audit.FullDisabled, rawRule)
			return
		}
		if len(bd.Domains) > 0 {
			audit.PartialDisabled++
			audit.PartialDisabledRaw = append(audit.PartialDisabledRaw, rawRule)
		}
	}
	nr.primaryStore.walkValues(func(r *rule.Rule) {
		visit(r.RawRule, r.BadfilterDisableState())
	})
	nr.exceptionStore.walkValues(func(er *exceptionrule.ExceptionRule) {
		visit(er.RawRule, er.BadfilterDisableState())
	})
	return audit
}
