package networkrules

// coveragededup.go — 2026-10-09 装载后覆盖去重（语义级）。
//
// 目标：同存储（阻断/例外）内，若规则 R 的匹配面是规则 S 的子集（S 覆盖
// R），且两者的行为签名一致，则 R 是冗余候选——在装载完成后物理删除，
// 归还规则对象与独占节点链内存，决策面不变（幸存者 S 在 R 的每个匹配
// URL 上都命中且产生相同效果）。
//
// 安全设计（评估报告《规则语义去重评估报告.md》2026-10-09，探针实测
// 158,233 组请求决策零差异）：
//   - 仅认"纯域名"（||d^ / ||d）与"域名rest"（||d/... 、||d^...）形态，
//     其余形态（通配符、正则、无 || 锚点）一律不参与——保守；
//   - 签名 = {例外侧, $document, $popup, $all, $important}。签名不同不互
//     相覆盖：无 $document 的普通阻断规则不拦用户导航（rule.go:496-497，
//     上游 #257 语义），hosts 派生规则拦导航出拦截页，二者不可合并；
//   - 仅认无条件修饰符、无动作/查询修饰符的规则（含条件/动作的包含关系
//     不做判定——那是另一层语义等价问题，保守排除）；
//   - 覆盖方向（tokenize.go:10-12 ^ 匹配"非字母/数字/_-.%＋地址末尾"、
//     ruletree.go:309-316 || 边界=hostStart＋host 内每个 '.'）：
//       ||d^   ⊆ ||a^ 与 ||a（a 为 d 的真标签后缀祖先）与 ||d（无^）
//       ||d    ⊆ ||a （仅无^祖先）
//       ||d/… 与 ||d^… ⊆ ||d^ / ||d / ||a^ / ||a
//     rest 形态只可被覆盖、永不作为覆盖者（保守）；
//   - 覆盖链解析到最终幸存者：幸存者带 $badfilter 禁用态（全禁用或域级
//     禁用，applyBadfilters 已在此前打标）⇒ 整链保守保留。语料实测该
//     过滤器拦下 1 条不安全去重（AdGuard Base 作者依赖宽窄规则共存＋
//     badfilter 精确瞄准，bf_audit §4.1）；
//   - 时序：ParseRule 装载期只记录（armed），Compact 在 applyBadfilters
//     之后一次性解析+剪枝并解除武装。Compact 后的活插入（白名单服务器
//     @@ 规则、热更新路径）永不参与、永不被删；
//   - 与列表增删改的关系：前端列表变更要求代理停止（前端门控），变更
//     在下次 StartProxy → buildFilter 全新构建时生效，去重随构建自动
//     重算——被删列表的覆盖者消失，此前被覆盖的规则自然恢复；
//   - 删除走 ruletree.PruneValues：叶内幸存值相对顺序不变，Get/GetLP
//     的确定性首现序列对幸存值保持不变；阻断者署名变化与 #804 同类
//     （"请求日志可能记任一列表"）。
//
// 开关：WithCoverageDedup(false)（默认开启）；装配层可用
// ZEN_RULE_COVER_DEDUP=0 一键回退，无需重建。

import (
	"log"
	"strings"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

// covSig 是覆盖等价签名：同签名 ⇒ 同导航豁免/拦截页/取消行为面。
type covSig struct {
	exc   bool // 例外存储（@@）
	doc   bool // $document
	popup bool // $popup
	all   bool // $all
	imp   bool // $important

	// 例外专用标志（exceptionrule.go:40-48）：任一置位的例外不参与去重
	// （见 covRecordException），并入签名是防御纵深——它们深度耦合
	// IsBare/HasPageScope/Cancels 的取消分类（B2 红线）。
	elemhide     bool
	generichide  bool
	specifichide bool
	genericblock bool
	urlblock     bool
	jsinject     bool
	content      bool
	extension    bool
	stealth      bool
}

// covKind 形态分类：0 不参与；1 纯域名+^；2 纯域名无^；3 域名rest。
type covKind uint8

const (
	covNone       covKind = 0
	covPureCaret  covKind = 1
	covPureBare   covKind = 2
	covDomainRest covKind = 3
)

type covRecPrimary struct {
	rule   *rule.Rule
	domain string
	kind   covKind
	sig    covSig
}

type covRecException struct {
	rule   *exceptionrule.ExceptionRule
	domain string
	kind   covKind
	sig    covSig
}

// classifyCovDomain 识别 || 前缀的纯域名/域名rest形态。
// 域名字符集 [A-Za-z0-9._-]（与评估探针一致）；遇到 '*'、':' 等其余
// 字符即不参与。返回 (域名, 形态)。
func classifyCovDomain(pattern string) (string, covKind) {
	if !strings.HasPrefix(pattern, "||") {
		return "", covNone
	}
	rest := pattern[2:]
	i := 0
	for ; i < len(rest); i++ {
		c := rest[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '-' || c == '_' {
			continue
		}
		break
	}
	if i == 0 {
		return "", covNone
	}
	domain := rest[:i]
	if i == len(rest) {
		return domain, covPureBare
	}
	switch rest[i] {
	case '^':
		if i == len(rest)-1 {
			return domain, covPureCaret
		}
		return domain, covDomainRest
	case '/':
		return domain, covDomainRest
	}
	return "", covNone
}

// covEligible 仅认无条件/无动作规则：含条件或动作修饰符的规则之间的
// 包含关系不做判定（保守排除）。
func covEligible(r *rule.Rule) bool {
	return len(r.AndConditionModifiers()) == 0 && len(r.OrConditionModifiers()) == 0 &&
		len(r.ActionModifiers()) == 0 && len(r.QueryModifiers()) == 0
}

func covSigOf(exc bool, r *rule.Rule) covSig {
	return covSig{exc: exc, doc: r.Document, popup: r.Popup, all: r.All, imp: r.Important}
}

// covSigOfException 在通用签名之上并入例外专用标志（防御纵深；
// 参与资格由 covRecordException 的排除检查先行把关）。
func covSigOfException(er *exceptionrule.ExceptionRule) covSig {
	sig := covSigOf(true, &er.Rule)
	sig.elemhide = er.Elemhide
	sig.generichide = er.Generichide
	sig.specifichide = er.Specifichide
	sig.genericblock = er.Genericblock
	sig.urlblock = er.URLBlock
	sig.jsinject = er.Jsinject
	sig.content = er.Content
	sig.extension = er.Extension
	sig.stealth = er.Stealth
	return sig
}

// covRecordPrimary 在 primaryStore.Insert 成功后登记一条候选。
// 未武装（Compact 已过）或开关关闭时直接返回：Compact 后的活插入
// （白名单服务器等）永不参与去重。
func (nr *NetworkRules) covRecordPrimary(r *rule.Rule, pattern string) {
	if !nr.covOn || !nr.covArmed.Load() {
		return
	}
	domain, kind := classifyCovDomain(pattern)
	if kind == covNone || !covEligible(r) {
		return
	}
	nr.covMu.Lock()
	nr.covP = append(nr.covP, covRecPrimary{rule: r, domain: domain, kind: kind, sig: covSigOf(false, r)})
	nr.covMu.Unlock()
}

// covRecordException 在 exceptionStore.Insert 成功后登记一条候选。
// 带例外专用修饰符（$elemhide/$generichide/$jsinject/$urlblock/$content/
// $genericblock/$specifichide/$extension/$stealth，exceptionrule.go:40-48）
// 的例外不参与去重：它们的取消/豁免面（IsBare、HasPageScope、Cancels 的
// scoped 分支、wNormAllBare 分类）与裸例外本质不同，而通用签名只覆盖
// rule.Rule 的标志——宁可漏删不可错删（评估报告口径：安全子集）。
func (nr *NetworkRules) covRecordException(er *exceptionrule.ExceptionRule, pattern string) {
	if !nr.covOn || !nr.covArmed.Load() {
		return
	}
	if er.Elemhide || er.Generichide || er.Specifichide || er.Genericblock ||
		er.URLBlock || er.Jsinject || er.Content || er.Extension || er.Stealth {
		return
	}
	domain, kind := classifyCovDomain(pattern)
	if kind == covNone || !covEligible(&er.Rule) {
		return
	}
	nr.covMu.Lock()
	nr.covE = append(nr.covE, covRecException{rule: er, domain: domain, kind: kind, sig: covSigOfException(er)})
	nr.covMu.Unlock()
}

// covSlot 记录 (签名, 域名) 槽位的两类所有者（装载顺序首个）。
type covSlot struct {
	caretOwner   int
	noCaretOwner int
}

// covAncestors 返回域名的真标签后缀祖先（近→远）。
func covAncestors(d string) []string {
	parts := strings.Split(d, ".")
	if len(parts) < 2 {
		return nil
	}
	res := make([]string, 0, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		res = append(res, strings.Join(parts[i:], "."))
	}
	return res
}

// covSurvivor 沿覆盖链走到终点（非 droppable 的幸存者）。
// 覆盖关系严格变宽，结构上无环；步数上限为防御性保险。
func covSurvivor(i int, coverer []int, droppable []bool) (int, bool) {
	cur, steps := i, 0
	for droppable[cur] && steps < 64 {
		cur = coverer[cur]
		steps++
	}
	return cur, steps < 64
}

// covResolve 是评估探针验证过的两遍算法：
// pass1 建槽位（owner=装载顺序首个）；pass2 判覆盖；链解析到幸存者后
// 检查幸存者的 $badfilter 禁用态——带禁用态（全禁用或域级禁用）⇒ 整链
// 保守保留。返回待删值集合。
func covResolve[R comparable](recs []R, domainOf func(R) string, kindOf func(R) covKind, sigOf func(R) covSig,
	state func(R) *rule.BadfilterDisable) map[R]struct{} {

	slots := make(map[covSig]map[string]*covSlot)
	slotFor := func(sig covSig, d string, create bool) *covSlot {
		sm, ok := slots[sig]
		if !ok {
			if !create {
				return nil
			}
			sm = make(map[string]*covSlot)
			slots[sig] = sm
		}
		sl, ok := sm[d]
		if !ok {
			if !create {
				return nil
			}
			sl = &covSlot{caretOwner: -1, noCaretOwner: -1}
			sm[d] = sl
		}
		return sl
	}

	// pass1：纯域名规则占槽（首个为 owner）。
	for i, rc := range recs {
		k := kindOf(rc)
		if k == covNone || k == covDomainRest {
			continue
		}
		sl := slotFor(sigOf(rc), domainOf(rc), true)
		if k == covPureCaret {
			if sl.caretOwner < 0 {
				sl.caretOwner = i
			}
		} else if sl.noCaretOwner < 0 {
			sl.noCaretOwner = i
		}
	}

	// pass2：全量判覆盖。
	coverer := make([]int, len(recs))
	droppable := make([]bool, len(recs))
	for i, rc := range recs {
		k := kindOf(rc)
		if k == covNone {
			continue
		}
		d := domainOf(rc)
		sig := sigOf(rc)
		sl := slotFor(sig, d, false)
		switch k {
		case covPureCaret:
			// 被同域无^、或任一真标签后缀祖先（任意^形态）覆盖。
			if sl != nil {
				if sl.caretOwner >= 0 && sl.caretOwner != i {
					coverer[i], droppable[i] = sl.caretOwner, true
				} else if sl.noCaretOwner >= 0 && sl.noCaretOwner != i {
					coverer[i], droppable[i] = sl.noCaretOwner, true
				}
			}
			if !droppable[i] {
				for _, a := range covAncestors(d) {
					asl := slotFor(sig, a, false)
					if asl == nil {
						continue
					}
					if asl.caretOwner >= 0 && asl.caretOwner != i {
						coverer[i], droppable[i] = asl.caretOwner, true
						break
					}
					if asl.noCaretOwner >= 0 && asl.noCaretOwner != i {
						coverer[i], droppable[i] = asl.noCaretOwner, true
						break
					}
				}
			}
		case covPureBare:
			// 无^形态只被同域无^或无^祖先覆盖（^祖先要求分隔符，非超集）。
			if sl != nil && sl.noCaretOwner >= 0 && sl.noCaretOwner != i {
				coverer[i], droppable[i] = sl.noCaretOwner, true
			}
			if !droppable[i] {
				for _, a := range covAncestors(d) {
					asl := slotFor(sig, a, false)
					if asl != nil && asl.noCaretOwner >= 0 && asl.noCaretOwner != i {
						coverer[i], droppable[i] = asl.noCaretOwner, true
						break
					}
				}
			}
		case covDomainRest:
			// ||d/…、||d^…：被 (d,^)、(d,无^) 或任一祖先覆盖；
			// rest 形态永不占槽、永不作为覆盖者（保守）。
			if sl != nil {
				if sl.caretOwner >= 0 {
					coverer[i], droppable[i] = sl.caretOwner, true
				} else if sl.noCaretOwner >= 0 {
					coverer[i], droppable[i] = sl.noCaretOwner, true
				}
			}
			if !droppable[i] {
				for _, a := range covAncestors(d) {
					asl := slotFor(sig, a, false)
					if asl == nil {
						continue
					}
					if asl.caretOwner >= 0 {
						coverer[i], droppable[i] = asl.caretOwner, true
						break
					}
					if asl.noCaretOwner >= 0 {
						coverer[i], droppable[i] = asl.noCaretOwner, true
						break
					}
				}
			}
		}
	}

	// 链解析＋badfilter 安全：幸存者带禁用态 ⇒ 整链保留。
	drop := make(map[R]struct{})
	for i := range recs {
		if !droppable[i] {
			continue
		}
		s, ok := covSurvivor(i, coverer, droppable)
		if !ok || state(recs[s]) != nil {
			continue
		}
		drop[recs[i]] = struct{}{}
	}
	return drop
}

// applyCoverageDedup 在 Compact（applyBadfilters 之后、store.Compact 之前）
// 执行一次：解析覆盖链并物理剪枝。幂等（armed CAS 保证只跑一次）；
// 调用契约与 applyBadfilters 相同——不得与 Insert/Get 并发。
func (nr *NetworkRules) applyCoverageDedup() {
	if !nr.covOn || !nr.covArmed.CompareAndSwap(true, false) {
		return
	}
	t0 := time.Now()
	nr.covMu.Lock()
	defer nr.covMu.Unlock()

	if len(nr.covP) == 0 && len(nr.covE) == 0 {
		return
	}

	dropP := covResolve(nr.covP,
		func(r covRecPrimary) string { return r.domain },
		func(r covRecPrimary) covKind { return r.kind },
		func(r covRecPrimary) covSig { return r.sig },
		func(r covRecPrimary) *rule.BadfilterDisable { return r.rule.BadfilterDisableState() })
	dropE := covResolve(nr.covE,
		func(r covRecException) string { return r.domain },
		func(r covRecException) covKind { return r.kind },
		func(r covRecException) covSig { return r.sig },
		func(r covRecException) *rule.BadfilterDisable { return r.rule.BadfilterDisableState() })

	removedP := 0
	if len(dropP) > 0 {
		dropPtrP := make(map[*rule.Rule]struct{}, len(dropP))
		for rec := range dropP {
			dropPtrP[rec.rule] = struct{}{}
		}
		removedP = nr.primaryStore.tree.PruneValues(func(r *rule.Rule) bool {
			_, ok := dropPtrP[r]
			return ok
		})
	}
	removedE := 0
	if len(dropE) > 0 {
		dropPtrE := make(map[*exceptionrule.ExceptionRule]struct{}, len(dropE))
		for rec := range dropE {
			dropPtrE[rec.rule] = struct{}{}
		}
		removedE = nr.exceptionStore.tree.PruneValues(func(er *exceptionrule.ExceptionRule) bool {
			_, ok := dropPtrE[er]
			return ok
		})
	}
	if removedP+removedE > 0 {
		log.Printf("networkrules: coverage dedup pruned %d primary + %d exception rules in %v",
			removedP, removedE, time.Since(t0))
	}
	nr.covTimeNanos.Store(int64(time.Since(t0)))
	nr.covP = nil
	nr.covE = nil
}
