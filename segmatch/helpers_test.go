package segmatch

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/ruletree"
)

func loadSegPatterns(t *testing.T) []string {
	t.Helper()
	f := `D:\Users\Administrator\Desktop\问题文件夹\Zen上游代理插件\测试临时\taxonomy_out\tree_patterns.tsv`
	fh, err := os.Open(f)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer fh.Close()
	var out []string
	sc := bufio.NewScanner(fh)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		p := strings.TrimSpace(strings.SplitN(sc.Text(), "\t", 2)[0])
		if p == "" || strings.Contains(p, "||") || strings.Contains(p, "|") {
			continue
		}
		if !strings.ContainsAny(p, "*^") {
			continue
		}
		out = append(out, p)
	}
	return out
}

func loadURLs(t *testing.T) []string {
	t.Helper()
	f := `D:\zen-ai-proto\internal\ruletree\testdata\urls.txt`
	fh, err := os.Open(f)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer fh.Close()
	var out []string
	sc := bufio.NewScanner(fh)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if u := strings.TrimSpace(sc.Text()); u != "" {
			out = append(out, u)
		}
	}
	return out
}

func segSample(t *testing.T, urls []string, n int) []string {
	t.Helper()
	var out []string
	for i, u := range urls {
		if i < 800 || i%13 == 0 {
			out = append(out, u)
		}
		if len(out) >= n {
			break
		}
	}
	out = append(out,
		"https://a-b.c_d%e.example.com/x^y*z?q=1&r=/2",
		"https://x^^^^y.test/a",
		"https://test/**/x/.js",
		"https://end.example.com/path^",
		"https://a.example.com/",
	)
	return out
}

// ruletreeNewOracle 建甲骨文树并返回查询闭包。
func ruletreeNewOracle(t *testing.T, pats []string) (*ruletree.Tree[string], func(string, string) bool) {
	t.Helper()
	tree := ruletree.New[string]()
	for _, p := range pats {
		tree.Insert(p, p)
	}
	tree.Compact()
	return tree, func(url, pat string) bool {
		for _, v := range tree.GetLP(url) {
			if v == pat {
				return true
			}
		}
		return false
	}
}

func treeHas(tree *ruletree.Tree[string], url, pat string) bool {
	for _, v := range tree.GetLP(url) {
		if v == pat {
			return true
		}
	}
	return false
}

func patIndex(pats []string, p string) int {
	for i := range pats {
		if pats[i] == p {
			return i
		}
	}
	return -1
}

func now() time.Time                  { return time.Now() }
func since(t time.Time) time.Duration { return time.Since(t) }
func timeDuration(d time.Duration) time.Duration {
	return d
}
