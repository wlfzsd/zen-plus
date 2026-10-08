package networkrules_test

// [2026-10-08 诊断探针] YouTube 广告回归排查：离线复现
// 装载（真实缓存名单）→ ActiveExceptions → asset.Handler 服务 scriptlets.js
// 的完整链路，定位 Engine.Inject 提前返回（Elemhide+Jsinject 双真）的触发规则。
// 仅当 ZEN_FILTER_CACHE_DIR 指向真实缓存目录时运行，CI 不受影响。

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/asset"
	"github.com/irbis-sh/zen-desktop/internal/exemption"
	"github.com/irbis-sh/zen-desktop/internal/filter"
	"github.com/irbis-sh/zen-desktop/internal/filterliststore"
	"github.com/irbis-sh/zen-desktop/internal/networkrules"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
	"github.com/irbis-sh/zen-desktop/internal/process"
)

// ── fakes for filter.NewFilter ──────────────────────────────────────────────

type probeObserver struct{}

func (probeObserver) OnFilterBlock(string, string, string, []rule.Rule, process.Info) {}
func (probeObserver) OnFilterRedirect(string, string, string, string, []rule.Rule, process.Info) {
}
func (probeObserver) OnFilterModify(string, string, string, []rule.Rule, process.Info) {}

type probeWhitelistSrv struct{}

func (probeWhitelistSrv) GetPort() int { return 0 }

type probeListStore struct{}

func (probeListStore) Get(context.Context, string, filterliststore.FetchMode) (io.ReadCloser, filterliststore.Source, error) {
	return nil, 0, io.EOF
}

// ── fixtures：config 启用名单 → 缓存文件（MD5(URL).cache.txt）+ trust ────────

type probeList struct {
	name    string
	url     string
	trusted bool
}

var probeLists = []probeList{
	{"Zen-Ads", "https://cdn.jsdelivr.net/gh/irbis-sh/filter-lists@master/ads/ads.txt", false},
	{"EasyList", "https://easylist.to/easylist/easylist.txt", false},
	{"AdGuard-Base", "https://raw.githubusercontent.com/AdguardTeam/FiltersRegistry/master/filters/filter_2_Base/filter.txt", true},
	{"Zen-Privacy", "https://cdn.jsdelivr.net/gh/irbis-sh/filter-lists@master/privacy/privacy.txt", false},
	{"EasyPrivacy", "https://easylist.to/easylist/easyprivacy.txt", false},
	{"AdGuard-Spyware", "https://raw.githubusercontent.com/AdguardTeam/FiltersRegistry/master/filters/filter_3_Spyware/filter.txt", true},
	{"URL-Shortener", "https://raw.githubusercontent.com/DandelionSprout/adfilt/master/LegitimateURLShortener.txt", false},
	{"Online-Malicious", "https://malware-filter.gitlab.io/malware-filter/urlhaus-filter-online.txt", false},
	{"Phishing", "https://malware-filter.gitlab.io/malware-filter/phishing-filter-hosts.txt", false},
	{"Dan-Pollock", "https://someonewhocares.org/hosts/zero/hosts", false},
	{"Peter-Lowe", "https://pgl.yoyo.org/adservers/serverlist.php?hostformat=hosts&showintro=1&mimetype=plaintext", false},
	{"AdGuard-CN", "https://filters.adtidy.org/extension/ublock/filters/224.txt", false},
	{"anti-AD", "https://anti-ad.net/easylist.txt", true},
}

func cacheFile(t *testing.T, dir string, l probeList) (string, bool) {
	sum := md5.Sum([]byte(l.url))
	name := filepath.Join(dir, hex.EncodeToString(sum[:])+".cache.txt")
	if _, err := os.Stat(name); err != nil {
		return "", false
	}
	return name, true
}

// buildFilter 复刻 app.buildFilter 的装载路径（filter.AddReader 路由全部规则）。
func buildFilter(t *testing.T, lists []probeList, dir string, extraLines []string) (*networkrules.NetworkRules, *asset.Engine) {
	t.Helper()
	nr := networkrules.New()
	eng, err := asset.NewEngine("assets.zen.local")
	if err != nil {
		t.Fatalf("asset.NewEngine: %v", err)
	}
	f, err := filter.NewFilter(nr, eng, probeListStore{}, probeObserver{}, probeWhitelistSrv{})
	if err != nil {
		t.Fatalf("filter.NewFilter: %v", err)
	}
	for _, l := range lists {
		name, ok := cacheFile(t, dir, l)
		if !ok {
			t.Logf("[probe] 缓存缺失，跳过 %s", l.name)
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := f.AddReader(strings.NewReader(string(data)), l.name, l.trusted); err != nil {
			t.Fatalf("AddReader %s: %v", l.name, err)
		}
	}
	if len(extraLines) > 0 {
		if err := f.AddReader(strings.NewReader(strings.Join(extraLines, "\n")), "probe-extra", true); err != nil {
			t.Fatalf("AddReader extra: %v", err)
		}
	}
	f.Finalize()
	return nr, eng
}

func activeExOf(t *testing.T, nr *networkrules.NetworkRules, rawURL string) exemption.Exemption {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, rawURL, nil)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	return nr.ActiveExceptions(req)
}

func fetchScriptletsAsset(t *testing.T, eng *asset.Engine, referer string) (string, int) {
	t.Helper()
	h := asset.NewHandler(eng)
	req := httptest.NewRequest(http.MethodGet, "https://assets.zen.local/scriptlets.js", nil)
	req.Header.Set("Referer", referer)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	count := strings.Count(body, "scriptlet(")
	return body, count
}

// ── 探针 1：全量装载 → youtube 三类页面的 ActiveExceptions ───────────────────

func TestZZProbeYouTubeExemptions(t *testing.T) {
	dir := os.Getenv("ZEN_FILTER_CACHE_DIR")
	if dir == "" {
		t.Skip("ZEN_FILTER_CACHE_DIR 未设置")
	}

	nr, eng := buildFilter(t, probeLists, dir, []string{"@@alicdn.com"})

	for _, u := range []string{
		"https://www.youtube.com/",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com/results?search_query=test",
	} {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("Sec-Fetch-Dest", "document")
		ex := nr.ActiveExceptions(req)
		t.Logf("[probe] ActiveExceptions(%s) = Elemhide:%v Generichide:%v Specifichide:%v Jsinject:%v",
			u, ex.Elemhide, ex.Generichide, ex.Specifichide, ex.Jsinject)
	}

	// 资产侧：浏览器视角拉 scriptlets.js
	body, count := fetchScriptletsAsset(t, eng, "https://www.youtube.com/")
	t.Logf("[probe] scriptlets.js for www.youtube.com: %d scriptlet 调用, %d bytes", count, len(body))
	for _, want := range []string{"json-prune-xhr-response", "json-prune-fetch-response", "json-prune", "set-constant"} {
		t.Logf("[probe]   含 %q: %v", want, strings.Contains(body, want))
	}
	os.WriteFile(filepath.Join(os.TempDir(), "probe-scriptlets-youtube.js"), []byte(body), 0o644)

	body2, count2 := fetchScriptletsAsset(t, eng, "https://www.youtube.com/")
	t.Logf("[probe] 二次拉取（模拟 ex=gh 之类不影响）: %d", count2)
	_ = body2
}

// ── 探针 2：单规则直测 + 全表二分，定位误命中的例外规则 ─────────────────────

// exFlags 装载 lines 后返回 youtube 首页的例外标志。
func exFlags(t *testing.T, lines []string) (eh, gh, sh, ji bool) {
	t.Helper()
	nr := networkrules.New()
	eng, err := asset.NewEngine("assets.zen.local")
	if err != nil {
		t.Fatalf("asset.NewEngine: %v", err)
	}
	f, err := filter.NewFilter(nr, eng, probeListStore{}, probeObserver{}, probeWhitelistSrv{})
	if err != nil {
		t.Fatalf("filter.NewFilter: %v", err)
	}
	if err := f.AddReader(strings.NewReader(strings.Join(lines, "\n")), "probe", true); err != nil {
		t.Fatalf("AddReader: %v", err)
	}
	f.Finalize()
	req := httptest.NewRequest(http.MethodGet, "https://www.youtube.com/", nil)
	req.Header.Set("Sec-Fetch-Dest", "document")
	ex := nr.ActiveExceptions(req)
	return ex.Elemhide, ex.Generichide, ex.Specifichide, ex.Jsinject
}

func TestZZProbeBisectExemption(t *testing.T) {
	dir := os.Getenv("ZEN_FILTER_CACHE_DIR")
	if dir == "" {
		t.Skip("ZEN_FILTER_CACHE_DIR 未设置")
	}

	// ① 头号嫌疑单测
	for _, suspect := range []string{
		"@@||youtube.com/my_video_ad$document",
		"@@||www.youtube.com^$generichide",
	} {
		eh, gh, sh, ji := exFlags(t, []string{suspect})
		t.Logf("[bisect] 单规则 %q → EH:%v GH:%v SH:%v JI:%v", suspect, eh, gh, sh, ji)
	}

	// ② 逐名单隔离
	var culpritName string
	var culpritLines []string
	for _, l := range probeLists {
		name, ok := cacheFile(t, dir, l)
		if !ok {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		eh, gh, sh, ji := exFlags(t, lines)
		if eh || gh || sh || ji {
			t.Logf("[bisect] 名单 %s → EH:%v GH:%v SH:%v JI:%v（行数 %d）", l.name, eh, gh, sh, ji, len(lines))
			if eh && ji {
				culpritName = l.name
				culpritLines = lines
			}
		}
	}
	if culpritName == "" {
		t.Log("[bisect] 未发现 EH+JI 双真名单")
		return
	}

	// ③ 名单内逐行扫描（EH+JI 双真规则可能只有几条，直接全扫最稳）
	var found []string
	for i, line := range culpritLines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "@@") {
			continue
		}
		eh, _, _, ji := exFlags(t, []string{trimmed})
		if eh && ji {
			t.Logf("[bisect] ❗ 误命中规则 [%s:%d] %q", culpritName, i+1, trimmed)
			found = append(found, trimmed)
		}
	}
	if len(found) == 0 {
		t.Log("[bisect] 单行扫描未复现，例外可能来自多行组合，需二分")
	}
}
