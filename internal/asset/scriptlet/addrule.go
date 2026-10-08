package scriptlet

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
)

var (
	// RuleRegex matches patterns for scriptlet rules in two formats:
	//
	//  1. #%#//scriptlet or #@%#//scriptlet for canonical rules.
	//  2. ##+js or #@#+js for uBlock-style rules.
	RuleRegex = regexp.MustCompile(`(?:#@?%#\/\/scriptlet)|(?:#@?#\+js)`)

	canonicalPrimary        = regexp.MustCompile(`(.*)#%#\/\/scriptlet\((.+)\)`)
	// [B4 2026-10-08] Exception rules may carry an empty argument list: a
	// nameless exception disables every scriptlet on the hostnames, so the
	// argument-list capture allows zero arguments. Primary rules still
	// require at least one.
	canonicalExceptionRegex = regexp.MustCompile(`(.*)#@%#\/\/scriptlet\((.*)\)`)
	ublockPrimaryRegex      = regexp.MustCompile(`(.*)##\+js\((.+)\)`)
	ublockExceptionRegex    = regexp.MustCompile(`(.*)#@#\+js\((.*)\)`)
	errUnsupportedSyntax    = errors.New("unsupported syntax")
	errUntrusted            = errors.New("trusted scriptlet in an untrusted filter list")
)

// trustedPrefix marks scriptlets that may only come from trusted filter lists.
const trustedPrefix = "trusted-"

// [B4 2026-10-08] nameAliases maps scriptlet name aliases to the canonical
// names the dispatch table is keyed by. Every entry is taken from the names
// array of the corresponding scriptlet in AdguardTeam/Scriptlets master
// (commit 49a3767), after the generic ".js"-suffix and "ubo-"/"abp-"-prefix
// normalization in normalizeScriptletName; e.g. the names array of
// prevent-setInterval.js contributes 'nosiif', 'sid', 'no-setInterval-if'
// and 'setInterval-defuser'. 'sto' is uBlock's own short alias of
// no-setTimeout-if and is not in AdGuard's names array, but subscription
// rules written for uBO carry it, so it is mapped as well.
var nameAliases = map[string]string{
	// abort-current-inline-script.js
	"acs":                  "abort-current-inline-script",
	"acis":                 "abort-current-inline-script",
	"abort-current-script": "abort-current-inline-script",
	// abort-on-property-read.js
	"aopr": "abort-on-property-read",
	// abort-on-property-write.js
	"aopw": "abort-on-property-write",
	// abort-on-stack-trace.js
	"aost": "abort-on-stack-trace",
	// adjust-setInterval.js
	"nano-setInterval-booster": "adjust-setInterval",
	"nano-sib":                 "adjust-setInterval",
	// adjust-setTimeout.js
	"nano-setTimeout-booster": "adjust-setTimeout",
	"nano-stb":                "adjust-setTimeout",
	// prevent-addEventListener.js
	"aeld":                     "prevent-addEventListener",
	"addEventListener-defuser": "prevent-addEventListener",
	// abp-prevent-listener, after the "abp-" prefix is stripped
	"prevent-listener": "prevent-addEventListener",
	// prevent-eval-if.js
	"noeval-if": "prevent-eval-if",
	// prevent-refresh.js
	"refresh-defuser": "prevent-refresh",
	// prevent-setInterval.js
	"no-setInterval-if":   "prevent-setInterval",
	"nosiif":              "prevent-setInterval",
	"sid":                 "prevent-setInterval",
	"setInterval-defuser": "prevent-setInterval",
	// prevent-setTimeout.js
	"no-setTimeout-if":   "prevent-setTimeout",
	"nostif":             "prevent-setTimeout",
	"setTimeout-defuser": "prevent-setTimeout",
	"std":                "prevent-setTimeout",
	"sto":                "prevent-setTimeout",
	// prevent-window-open.js
	"no-window-open-if":  "prevent-window-open",
	"window.open-defuser": "prevent-window-open",
	// remove-attr.js
	"ra": "remove-attr",
	// remove-class.js
	"rc": "remove-class",
	// remove-cookie.js
	"cookie-remover": "remove-cookie",
	// remove-node-text.js
	"rmnt": "remove-node-text",
	// set-constant.js
	"set": "set-constant",
	// abp-override-property-read, after the "abp-" prefix is stripped
	"override-property-read": "set-constant",
	// log-addEventListener.ts
	"aell":                     "log-addEventListener",
	"addEventListener-logger":  "log-addEventListener",
}

// normalizeScriptletName reduces a scriptlet name argument to the form the
// dispatch table is keyed by: the ".js" suffix of file-style aliases (e.g.
// 'aopr.js') and the "ubo-"/"abp-" engine-compatibility prefixes (e.g.
// 'ubo-aopr', 'abp-abort-current-inline-script') are stripped, then short
// aliases are mapped to the canonical name from the official names arrays.
// The trusted-name gate below, the stored argument list and exception
// matching all see the normalized name, so equal rules compare equal
// regardless of the alias they were written with.
func normalizeScriptletName(name string) string {
	normalized := strings.TrimSpace(name)
	if suffix := ".js"; strings.HasSuffix(normalized, suffix) {
		normalized = strings.TrimSuffix(normalized, suffix)
	}
	for _, prefix := range []string{"ubo-", "abp-"} {
		if rest, ok := strings.CutPrefix(normalized, prefix); ok {
			normalized = rest
			break
		}
	}
	if canonical, ok := nameAliases[normalized]; ok {
		return canonical
	}
	return normalized
}

// [P3 2026-10-08] knownScriptletNames mirrors the dispatch table of the
// bundled scriptlet engine: the 44 keys of the "new Map([[...]])" table at
// the tail of bundle.js (canonical names and the raw short keys the bundle
// registers directly, in bundle order). A stored rule whose normalized name
// is absent from this list has no implementation and runs inert; AddRule
// reports it once (comp_audit TOP-5 observability).
var knownScriptletNames = []string{
	"abort-current-inline-script",
	"abort-on-property-read",
	"aopr",
	"abort-on-property-write",
	"aopw",
	"abort-on-stack-trace",
	"aost",
	"json-prune",
	"nowebrtc",
	"prevent-fetch",
	"no-fetch-if",
	"prevent-xhr",
	"no-xhr-if",
	"set-local-storage-item",
	"set-session-storage-item",
	"set-constant",
	"json-prune-fetch-response",
	"json-prune-xhr-response",
	"prevent-window-open",
	"nowoif",
	"prevent-setTimeout",
	"prevent-setInterval",
	"prevent-addEventListener",
	"no-topics",
	"no-protected-audience",
	"sanitize-clipboard",
	"prevent-element-src-loading",
	"remove-node-text",
	"remove-attr",
	"remove-class",
	"set-attr",
	"set-cookie",
	"set-cookie-reload",
	"remove-cookie",
	"prevent-eval-if",
	"prevent-refresh",
	"adjust-setTimeout",
	"adjust-setInterval",
	"prevent-innerHTML",
	"prevent-navigation",
	"log",
	"log-eval",
	"log-addEventListener",
	"log-on-stack-trace",
}

func isKnownScriptletName(name string) bool {
	for _, n := range knownScriptletNames {
		if n == name {
			return true
		}
	}
	return false
}

// [P3 2026-10-08] warnedScriptletNames deduplicates the inert-name warning so
// a recurring unknown rule cannot flood the log; each distinct normalized
// name is reported once per process.
var warnedScriptletNames sync.Map

// warnInertScriptlet logs a stored rule whose normalized name has no
// implementation. Trusted-prefixed names get their own wording; the trusted-
// decision runs on the normalized name (AddRule normalized args[0] earlier).
func warnInertScriptlet(name string) {
	if isKnownScriptletName(name) {
		return
	}
	if _, loaded := warnedScriptletNames.LoadOrStore(name, struct{}{}); loaded {
		return
	}
	if strings.HasPrefix(name, trustedPrefix) {
		log.Printf("trusted scriptlet not yet implemented: %q (rule inert)", name)
		return
	}
	log.Printf("scriptlet %q has no implementation (rule inert)", name)
}

func (inj *Injector) AddRule(rule string, filterListTrusted bool) error {
	var body string
	var isUblock, isException bool
	var hostnamePatterns string

	if match := canonicalPrimary.FindStringSubmatch(rule); match != nil {
		hostnamePatterns, body = match[1], match[2]
	} else if match := canonicalExceptionRegex.FindStringSubmatch(rule); match != nil {
		hostnamePatterns, body = match[1], match[2]
		isException = true
	} else if match := ublockPrimaryRegex.FindStringSubmatch(rule); match != nil {
		hostnamePatterns, body = match[1], match[2]
		isUblock = true
	} else if match := ublockExceptionRegex.FindStringSubmatch(rule); match != nil {
		hostnamePatterns, body = match[1], match[2]
		isUblock = true
		isException = true
	} else {
		return errUnsupportedSyntax
	}

	var args []string
	if isException && strings.TrimSpace(body) == "" {
		// [B4 2026-10-08] An empty argument list on an exception rule is a
		// nameless exception: it disables every scriptlet on the hostnames.
		// It encodes to an empty argList, which the exception matcher treats
		// as a wildcard. Primary rules still require a name, so they reject
		// an empty list via the parsers below.
		args = nil
	} else {
		var err error
		if isUblock {
			args, err = parseUboArgList(body)
		} else {
			args, err = parseCanonicalArgList(body)
		}
		if err != nil {
			return fmt.Errorf("parse argument list: %v", err)
		}
	}

	// [B4 2026-10-08] The name is normalized before anything that depends on
	// it: the trusted-name gate and the stored argument list both see the
	// canonical form, so a "trusted-" name stays gated no matter how it was
	// spelled, and alias spellings of one rule compare equal.
	if len(args) > 0 {
		args[0] = normalizeScriptletName(args[0])
	}

	if len(args) > 0 && !filterListTrusted && strings.HasPrefix(args[0], trustedPrefix) {
		return errUntrusted
	}

	al, err := newArgList(args)
	if err != nil {
		return fmt.Errorf("encode argument list: %v", err)
	}

	// [P3 2026-10-08] 可观测性：走到这里的规则已确定入库，但名字若不在
	// bundle 分发表里则运行期惰性（bundle 只 debug 一句）。按归一化名
	// 去重记 warn（comp_audit TOP-5）；trusted-* 判定基于归一化后的名字。
	if len(args) > 0 {
		warnInertScriptlet(args[0])
	}

	switch isException {
	case true:
		inj.store.AddExceptionRule(hostnamePatterns, al)
	case false:
		inj.store.AddPrimaryRule(hostnamePatterns, al)
	}

	return nil
}
