package nrprobe

// Focused extractor diagnostics for specific real-corpus bodies.
import (
	"fmt"
	"regexp/syntax"
	"testing"
)

func TestDebugExtractor(t *testing.T) {
	bodies := []string{
		`[Ww]eb[Tt]racking`,
		`(toonget|kickassanime|watchanime|gogoanimes?)\.[a-z]{2,4}\/[s\S]*\/[-_a-zA-Z0-9]{22,}`,
		`(toonget|kickassanime|watchanime|(the)?watchcartoons?online|readcomiconline|kimcartoon|kiss-anime|9anime|gogoanimes?|toonova|animeflv|animeplus|goodanime|animewow|animenova|animetoon|mangapanda|memecenter|mangareader|mangafreak)\.[a-z]{2,4}\/[s\S]*\/[-_a-zA-Z0-9]{22,}`,
		`(readcomiconline|kimcartoon|kiss-anime|9anime|gogoanime|toonova|animeflv|animeplus|goodanime|animewow|animenova|animetoon|mangapanda|memecenter|mangareader)\.[a-z]{2,4}\/[-_a-zA-Z0-9]{3,}\/[-_a-zA-Z0-9]{20,}.(gif|jpg|png)`,
		`\/[a-zA-Z0-9-]{0,9}(?:clobew|-host-|alex|privalps|wcor|waitj|host3|avahosting|servhost|azlogo|luxhst)[a-zA-Z0-9-]{0,9}\.[a-z]{3}`,
		`js/sextb.js$domain=sextb.date|sextb.net,replace=/if\(checkads\(\)==false\)/if(!0)`,
		`^(\S+\.)?(webstats?|swebstats?|mywebstats?)\.`,
	}
	for _, b := range bodies {
		ast, err := syntax.Parse(b, syntax.Perl)
		if err != nil {
			t.Logf("PAT %q parse err %v", b, err)
			continue
		}
		segs, lks := segmentsOfNode(ast)
		brs := altTokens(ast)
		fmt.Printf("PAT %.60q...\n  root=%v fold=%v segs=%v locked=%v altTokens=%v\n", b, ast.Op, hasFoldCase(ast), segs, lks, brs)
		if brs == nil && ast.Op == syntax.OpConcat {
			for i, c := range ast.Sub {
				fmt.Printf("   child[%d] op=%v mandatory=%v altGroup=%v\n", i, c.Op, isMandatory(c), altGroupOf(c) != nil)
			}
		}
		// branch-level debug for the failing cases
		var group *syntax.Regexp
		switch ast.Op {
		case syntax.OpAlternate:
			group = ast
		case syntax.OpConcat:
			for _, c := range ast.Sub {
				if isMandatory(c) {
					if g := altGroupOf(c); g != nil {
						group = g
						break
					}
				}
			}
		}
		if group != nil {
			for j, b := range group.Sub {
				ts, ok := branchOneOf(b)
				fmt.Printf("   branch[%d] op=%v oneOf=%v ok=%v\n", j, b.Op, ts, ok)
			}
		}
	}
}

func dumpTree(r *syntax.Regexp, depth int) {
	ind := ""
	for i := 0; i < depth; i++ {
		ind += "  "
	}
	fmt.Printf("%sop=%v flags=%d rune=%q sub=%d\n", ind, r.Op, r.Flags, string(r.Rune), len(r.Sub))
	for _, s := range r.Sub {
		dumpTree(s, depth+1)
	}
}
