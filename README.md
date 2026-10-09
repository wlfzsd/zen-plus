<div align="center">

# zen-plus

**中文** | [English](#english)

</div>

---

## 中文

**zen-plus 是 [Zen](https://github.com/irbis-sh/zen-desktop) 的增强分支**：一款开源的系统级广告拦截与隐私防护应用。zen-plus 在上游基础上，对**过滤规则匹配引擎**做了两轮深度性能优化——拦截能力与规则兼容性与上游完全一致，而引擎执行效率与内存占用大幅下降。

> 上游归属：本项目基于 [irbis-sh/zen-desktop](https://github.com/irbis-sh/zen-desktop)（MIT License，Copyright (c) 2026 Irbis & Zen contributors）。全部上游提交历史完整保留在 `master` 分支，本地增强在 `upstream-chain` 分支。所有修改以 MIT 协议回馈社区。

### 引擎优化了什么 / What was optimized

两轮优化均以"**语义零变化**"为硬约束——每一步都有与改前引擎的逐请求对拍（黄金回放 3.6 万×2 全一致、1 万真实 URL 等价 0 差异、20 万随机 URL 模糊 0 差异、32 线程并发压力 0 差异）：

1. **正则形状特化**（`internal/networkrules/fastshape.go`）：对 `^https?://…` 等常见正则形状在解析期生成精确等价的快速匹配器，未识别形状回落原 regexp 引擎；
2. **最稀 token 反向索引**（`internal/networkrules/tokenindex.go`，借鉴 Ghostery adblocker 技巧）：对回落的正则按"必需字面量"建索引，URL 不含 token 的正则直接跳过（零漏报证明见 `docs/benchmarks/probe-report.md`）；
3. **请求级缓存**：Referer 主机名、有效 TLD+1、用户导航守卫等每请求只算一次；
4. **内存结构瘦身**（`internal/networkrules/rule`、`internal/ruletree`）：规则对象 4 个切片头 → 2 个惰性指针，树节点 96B → 64B；
5. **低分配遍历**：树遍历共享累加器、去重 map 与结果切片池化，每请求分配从 1175 次降至 225 次。

### 相比上游的修复与增强 / Fixes & enhancements beyond upstream

- **修复：过滤开关反复切换导致内存大幅增长**（上游 bug）。上游在禁用/启用规则拦截时，内层连接服务器、退休的证书生成器与双向隧道不被回收，内存逐次翻倍；zen-plus 在 Stop 路径全部收割（提交 `81efce0`、`fb7e6a7`），并为传输空闲池设上限、空闲内存定期归还 OS。
- **新增：上游代理链**（支持 HTTP / HTTPS / SOCKS5 上游，可带凭证）。在应用内 设置 → 高级 中直接配置（存入 config.json，旧 `upstream-proxy.txt` 首次启动自动迁移），全部代理流量再经上游转发，用于叠加自有网络出口；目标主机名不在本地解析——DNS 留在上游，设置还会镜像到标准代理环境变量，过滤列表下载同样走链。镜像架构：拨号失败按新规格重拨镜像重试，**无任何静默降级路径**。
- **新增：TLS 指纹镜像与出站协议重放**（`internal/proxy/uptls.go`、`internal/proxy/reoriginate.go`）。MITM 后重新出站的 TLS 连接镜像浏览器自己的 ClientHello 结构指纹（uTLS）：Chrome 出去就是 Chrome 指纹、Firefox 就是 Firefox——不指定、不伪造任何档案。实测同一请求经 Zen 与直连**出口 JA3 逐字节一致**，规避"代理 TLS 指纹错配"类网站封控。v3.1.0 起出站协议跟随客户端：HTTP/2 客户端按捕获的 h2 指纹参数（SETTINGS 值与顺序、连接级 WINDOW_UPDATE、PRIORITY 帧、伪头与头部顺序）在 HTTP/2 上重放，HTTP/1.1 客户端保持 HTTP/1.1。已知边界：ALPS/ECH 扩展剔除、后量子混合密钥交换组被裁（Go 无法生成对应 key share）、h2 重放不含 PRIORITY_UPDATE 且不控制 HPACK 索引表示（详见源码头注）。
- **增强：AdGuard 规则语义兼容**（B1-B8 兼容批次）。例外语义重构（逐请求取消、页面级豁免）、popup/$cookie/$script:inject 别名与批量语法补齐；`$jsonprune` 方言预处理与 trusted-* 脚本注入键扩充，激活一批此前解析失败或静默失效的规则；修复响应路径判定（`$xmlhttprequest`/`$domain` 条件此前在响应侧恒不命中、`$permissions` 曾误加到全部响应、`$document` 异常作用域曾导致脚本注入全站失效）。
- **增强：引擎性能与健壮性**。超长/组合 URL 遍历爆炸修复（68KB URL 从分钟级/183MB 内存降至毫秒级）、同签名规则覆盖去重（默认列表裁剪约 1.46 万条、live 堆 -3.3MB、决策 0 差异）、match-all `$cookie`/`$removeparam` 动作索引与 ModifyReq 热路径提速；每项变更均附与改前引擎的逐请求对拍证据。

### 实测效果 / Measured results（真实订阅语料 61.1 万条规则，多轮中位数）

| 指标 Metric | 上游基线 Upstream | zen-plus | 变化 Δ |
|---|---|---|---|
| 引擎匹配耗时 Matching time / request | 370,336 ns | **145,651 ns** | **-60.7%** |
| 每请求堆分配 Allocations / request | 1,175 次 / 132 KB | **225 次 / 8.7 KB** | **-80.9% / -93.4%** |
| 引擎 live 内存 Engine live memory | 494 B/规则 rule | **388 B/规则** | **-21.5%** |
| 过滤列表装载 List loading | 560 ms | 521 ms | -6.9% |
| 拦截有效性（d3ward 测试页得分） | 96.2% | **96.2%** | 完全一致 identical |

完整数据、测试方法与复现步骤见 [docs/benchmarks/](docs/benchmarks/)（中英对照汇总：[benchmark-summary.md](docs/benchmarks/benchmark-summary.md)）。

### 安装 / Install

从 [Releases](https://github.com/wlfzsd/zen-plus/releases) 下载 `Zen.exe`（附 SHA256 校验值；可执行文件未做代码签名，SmartScreen 提示属正常）。覆盖安装到 `%LOCALAPPDATA%\Programs\Zen\` 后启动即可，配置与过滤器缓存沿用原版。

### 版本策略 / Versioning

zen-plus 使用自己的版本线（当前 **3.1.0**，起点为上游 v0.25.1 基线）；每个版本对应的上游基线写进 Release 说明。exe 内的版本号由构建时的 git tag 自动注入。
zen-plus keeps its own version line (currently **3.1.0**, starting from the upstream v0.25.1 baseline); the upstream base of every release is stated in its release notes. The binary version string is injected from the git tag at build time.

### 本地构建 / Build from source

```bash
# 需要 Go 1.27+ 与 Node.js 24+
wails build -o Zen.exe -platform windows/amd64 -tags prod
# 产物：build/bin/Zen.exe
```

### 验证装置 / Verification harness

`tmp_enginebench/` 是全部性能与正确性结论的复现装置（冻结基线对拍、黄金回放、并发压力、广告测试页数据集、真实订阅语料装载器），用法见 [tmp_enginebench/README.md](tmp_enginebench/README.md)。

### License

MIT（保留上游 [LICENSE](LICENSE)）。上游项目与团队：[irbis-sh/zen-desktop](https://github.com/irbis-sh/zen-desktop) • [irbis.sh](https://irbis.sh/zen)。

---

<a id="english"></a>

## English

**zen-plus is an enhanced fork of [Zen](https://github.com/irbis-sh/zen-desktop)**, an open-source system-wide ad-blocker and privacy guard. On top of upstream, zen-plus ships two rounds of deep performance optimizations to the **filter-rule matching engine** — blocking behavior and rule compatibility are bit-for-bit identical to upstream, while engine CPU time and memory drop substantially.

> Attribution: based on [irbis-sh/zen-desktop](https://github.com/irbis-sh/zen-desktop) (MIT License, Copyright (c) 2026 Irbis & Zen contributors). The complete upstream commit history is preserved on the `master` branch; local enhancements live on `upstream-chain`.

### What was optimized

Both rounds are hard-constrained by **zero semantic change** — every step was verified against the pre-optimization engine request-by-request (golden replay: 36,231×2 exact matches; 10k real-URL equivalence: 0 diffs; 200k random-URL fuzz: 0 diffs; 32-thread stress: 0 diffs):

1. **Regexp shape specialization** (`internal/networkrules/fastshape.go`): common shapes like `^https?://…` are compiled at parse time into exact-equivalent fast matchers; unrecognized shapes fall back to `regexp.Regexp`;
2. **Rarest-token reverse index** (`internal/networkrules/tokenindex.go`, technique credited to the Ghostery adblocker): fallback regexps are indexed by their provably-required literals; a URL missing the token skips the regexp entirely (zero false negatives — proof in `docs/benchmarks/probe-report.md`);
3. **Per-request caches**: referer hostname, effective TLD+1, and the user-navigation guard are computed once per request instead of once per candidate rule;
4. **Memory layout slimming** (`internal/networkrules/rule`, `internal/ruletree`): rule structs went from 4 slice headers to 2 lazy pointers; tree nodes from 96B to 64B;
5. **Low-allocation traversal**: shared traversal accumulator plus pooled dedup maps and result slices — per-request allocations dropped from 1,175 to 225.

### Fixes & enhancements beyond upstream

- **Fixed: large memory growth when toggling ad-blocking on/off** (upstream bug). Upstream leaks per-connection servers, retired certificate generators and bidirectional tunnels across filter toggles; zen-plus reaps all of them on Stop (commits `81efce0`, `fb7e6a7`), caps transport idle pools and returns idle memory to the OS periodically.
- **Added: upstream proxy chain** (HTTP, HTTPS and SOCKS5 upstreams, optional credentials). Configure it in the app under Settings → Advanced (stored in config.json; a legacy `upstream-proxy.txt` is imported once on first start) to route all proxied traffic through an upstream of your choice; target hostnames are left unresolved locally — DNS stays at the upstream — and the setting is mirrored into the standard proxy environment variables so filter-list downloads chain too. Mirror architecture retries dialing with a fresh spec — **no silent fallback path**.
- **Added: TLS fingerprint mirroring and outbound protocol replay** (`internal/proxy/uptls.go`, `internal/proxy/reoriginate.go`). Re-emitted TLS connections after MITM mirror the browser's **own ClientHello structural fingerprint** (uTLS): Chrome goes out as Chrome, Firefox as Firefox — no profile is invented or selected. Verified: JA3 through Zen is **identical, byte-for-byte, to a direct connection**, defusing the "proxy TLS fingerprint mismatch" class of site blocks. Since v3.1.0 the outbound protocol follows the client: an HTTP/2 client is re-originated over HTTP/2 from the captured h2 fingerprint parameters (SETTINGS values and order, connection-level WINDOW_UPDATE, PRIORITY frames, pseudo-header and header order); an HTTP/1.1 client stays on HTTP/1.1. Known limits: ALPS/ECH extensions dropped, post-quantum key-share groups dropped (Go cannot generate the corresponding shares), h2 replay emits no PRIORITY_UPDATE and no HPACK index-representation control (see the source-file header notes).
- **Enhanced: AdGuard rule-semantics compatibility** (batches B1–B8). Reworked exception semantics (per-request cancellation, page-scope exemptions), popup/$cookie/$script:inject aliases and broad syntax coverage; `$jsonprune` dialect preprocessing plus trusted-* scriptlet keys activate rules that previously failed to parse or were silently inert; fixed response-path conditions (`$xmlhttprequest`/`$domain` never matched there, `$permissions` was applied to every response, `$document` exception scoping silently disabled scriptlet injection site-wide).
- **Enhanced: engine performance and robustness**. Traversal-explosion fix for very long / combinatorial URLs (a 68KB URL went from minutes and 183MB to milliseconds), same-signature coverage dedup (~14.6k rules pruned on the default lists, 3.3MB live heap, zero decision diffs), a match-all `$cookie`/`$removeparam` action index and a faster ModifyReq hot path; every change ships with request-by-request replay evidence against the previous engine.

### Measured results (real-subscription corpus, 611,652 rules, medians)

| Metric | Upstream baseline | zen-plus | Δ |
|---|---|---|---|
| Engine matching time / request | 370,336 ns | **145,651 ns** | **-60.7%** |
| Heap allocations / request | 1,175 / 132 KB | **225 / 8.7 KB** | **-80.9% / -93.4%** |
| Engine live memory | 494 B/rule | **388 B/rule** | **-21.5%** |
| Filter-list loading | 560 ms | 521 ms | -6.9% |
| Blocking effectiveness (d3ward test page) | 96.2% | **96.2%** | identical |

Full data, methodology and reproduction steps: [docs/benchmarks/](docs/benchmarks/) (bilingual summary: [benchmark-summary.md](docs/benchmarks/benchmark-summary.md)).

### Install

Grab `Zen.exe` from [Releases](https://github.com/wlfzsd/zen-plus/releases) (SHA256 attached; binaries are unsigned, so SmartScreen may warn). Replace `%LOCALAPPDATA%\Programs\Zen\Zen.exe` and start — configuration and filter caches are shared with upstream builds.

### Build from source

```bash
# Requires Go 1.27+ and Node.js 24+
wails build -o Zen.exe -platform windows/amd64 -tags prod
# Output: build/bin/Zen.exe
```

### Verification harness

`tmp_enginebench/` reproduces every performance and correctness claim (frozen-baseline A/B, golden replay, concurrency stress, ad-test-page datasets, real-subscription corpus loader) — see [tmp_enginebench/README.md](tmp_enginebench/README.md).

### License

MIT (upstream [LICENSE](LICENSE) preserved). Upstream project and team: [irbis-sh/zen-desktop](https://github.com/irbis-sh/zen-desktop) • [irbis.sh](https://irbis.sh/zen).
