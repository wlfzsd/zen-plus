<div align="center">

# zen-plus

**中文** | [English](#english)

</div>

---

## 中文

# zen-plus：比官方最新版更快、更省、更全能的 Zen

**zen-plus 是 [Zen](https://github.com/irbis-sh/zen-desktop) 的增强分支**。同一套规则、同一台机器、同一批真实 URL 实测：**过滤引擎快 35%，每请求内存分配少 73 倍，驻留内存少 10.5%——而且是在多装了 9,490 条规则的前提下**。官方最新版（v0.26.0）没有的能力——TLS 指纹镜像、HTTP/2 出站重放、上游代理链——zen-plus 全都有。

> 上游归属：本项目基于 [irbis-sh/zen-desktop](https://github.com/irbis-sh/zen-desktop)（MIT License，Copyright (c) 2026 Irbis & Zen contributors）。上游全部提交历史完整保留在 `master` 分支，本地增强在 `upstream-chain` 分支。

---

### 一张表看懂 zen-plus vs 官方最新版 v0.26.0

实测条件：同一台机器、同一套 13 个过滤列表（54.5 万条规则生产同构装载）、35,358 条真实 URL/遍、5 轮取中位、单线程固定。拦截判定测试全集 0 失败。

| 对比项 | 官方 v0.26.0 | **zen-plus v3.1.0** | 结果 |
|---|---|---|---|
| 引擎匹配速度（每请求） | 234.8 µs | **153.1 µs** | **zen-plus 快 34.8%** |
| 引擎匹配速度（XHR/fetch 型） | 282.4 µs | **177.2 µs** | **zen-plus 快 37.3%** |
| 每请求内存分配 | 149.3 KB / 1,625 次 | **2.03 KB / 13 次** | **分配少 73 倍 / 124 倍** |
| 引擎驻留内存（装载后 GC） | 205.5 MB | **184.0 MB** | **-10.5%** |
| 实际装载规则数 | 535,887 | **545,377** | **多装 9,490 条** |
| 规则解析失败（静默丢弃） | 11,867 | **2,377** | **少丢 9,490 条** |
| 相同流量 CPU 开销 | 3.91 ms/req | **2.71 ms/req** | **CPU 低 31%** |
| TLS 指纹一致性 | ✗ 无此功能 | **JA3 与直连逐字节一致** | **官方完全缺失** |
| HTTP/2 出站重放 | ✗ | ✅ | **官方完全缺失** |
| 上游代理链 | ✗ | ✅ HTTP/HTTPS/SOCKS5 | **官方完全缺失** |
| 拦截覆盖（obfusgated 359 域名实测） | 604 点 / 302 域 | **610 点 / 305 域** | **zen-plus ⊇ 官方，0 反超** |

---

### ✊ 内存那行 "-10.5%" 背后的真相：少 10% 内存，多 9,490 条规则

单看 "-10.5%" 好像不大——**但这是在 zen-plus 多装载了 9,490 条规则的同时实现的**。

官方 v0.26.0 的解析器会把 **11,867 条规则静默丢弃**（解析失败零报错），zen-plus 只丢 2,377 条。差距在哪：EasyList **3,139 vs 7**、AdGuard Base **6,314 vs 1,394**、AdGuard Spyware **1,465 vs 327**、AdGuard CN **903 vs 609**。这些被官方丢掉的规则里，就包括拦截手机厂商广告域的 `$denyallow` 规则——实测 oppo 广告域名（adx/ck/data.ads.oppomobile.com）官方放行、zen-plus 拦截，且 zen-plus 的拦截覆盖**严格包含**官方（604 个命中点 zen-plus 全拦，无一反超）。

**多装近一万条规则 + 更省内存 + 更快匹配**，三件事同时成立，这就是引擎重写的价值。

---

### 🛡️ 指纹一致性：这个功能救过作者自己的号

**真实案例：作者此前用原版开 1688，一直被风控封号。** 反复排查后定位到根因——原版代理在 MITM 之后，出站 TLS 用的是 Go 标准库的 ClientHello 指纹，**与浏览器自己的指纹完全错配**，风控系统一眼识别"代理流量"。这个问题极其隐蔽（表现就是"代理明明能用却总被封"），也极其致命。

zen-plus 的解法是 **TLS 指纹镜像**（`internal/proxy/uptls.go`）：MITM 后重新出站的 TLS 连接，镜像浏览器自己的 ClientHello 结构——**Chrome 出去就是 Chrome 的指纹，Firefox 出去就是 Firefox 的指纹，不指定、不伪造任何档案**。实测同一请求经 zen-plus 与直连，**出口 JA3 逐字节一致**。v3.1.0 起更进一步：HTTP/2 客户端按捕获的 h2 指纹参数（SETTINGS 值与顺序、WINDOW_UPDATE、PRIORITY 帧、伪头与头部顺序）在 HTTP/2 上完整重放（`internal/proxy/reoriginate.go`）。

**官方 v0.26.0 至今没有这个能力**（源码级核实：无对应实现）。只要你还用任何代理出去，这类"代理指纹识别"风控就是悬在头上的剑——zen-plus 把这把剑拿掉了。已知边界（ALPS/ECH 剔除等）见源码头注。

---

### 🌏 上游代理链：国内上网的一站式答案

**官方版完全没有上游代理功能**（源码级核实）。想"系统级去广告 + 自己的代理出口"两个都要？官方版做不到，zen-plus 原生支持：

- **HTTP / HTTPS / SOCKS5 上游，可带凭证**，应用内 设置 → 高级 直接配置；
- 全部代理流量再经你的上游转发——**你的 Clash / sing-box / 机场节点与 zen-plus 无缝叠加**；
- **DNS 留在上游解析**，过滤列表下载同样走链，设置镜像到标准代理环境变量；
- 拨号失败按新规格重拨镜像重试，**没有任何静默降级路径**——该是代理就是代理，不会悄悄直连。

实测代价透明：国内站点经上游链热连接仅多 2~6ms（多数站点 ±10ms 内、无感），冷连接多约 14ms——换来的是**去广告 + 私有出口一站式**，不用再在"系统代理"与"去广告"之间二选一。

---

### 🧩 AdGuard 兼容：以官方语义为目标，能接住 AdGuard 的规则

zen-plus 以 **AdGuard 官方过滤语法语义**为对齐目标逐批实现：例外语义（逐请求取消、`$document` 页面豁免）、`$badfilter` / `$removeparam` / `$replace` / `$csp` / `$permissions` / `$cookie`、`$jsonprune` AdGuard 方言全量预处理（未加引号联合、`?(has/…)` 条件、`[-]`/`{-}`/`\$..` 等扩展路径）、12 个 `trusted-*` 脚本注入键、响应路径 `$xmlhttprequest`（Fetch Metadata 判定）、`$domain` 响应侧判定（180 条死规则复活）等。每批语法变更都以 83 万行真实语料逐行归因对拍（0 未归因）。

结果就是上面那张表：**官方丢掉的近一万条规则（多数是 AdGuard 系列表的），在 zen-plus 里全部生效**。再叠加 zen-plus 是**系统级**拦截——不只浏览器，所有应用的流量都过引擎。浏览器扩展 + 系统级方案（AdGuard for Windows 那类）能做的，zen-plus 一个应用做，而且规则吃得更多。

---

### 📌 附：对更早基线（v0.25.1）的历史实测

zen-plus 引擎重写的完整收益在对官方 v0.26.0 的上表中；相对更早的 v0.25.1 基线端到端实测为：匹配 -60.7%、分配 -93.4%、每规则内存 -21.5%（多轮中位，明细见 [docs/benchmarks/](docs/benchmarks/)）。此外 zen-plus 修复了两个上游至今（v0.26.0 未修，源码级核实）的问题：

- **过滤开关反复切换导致内存翻倍增长**（上游每次开关泄漏连接服务器/证书生成器/隧道，zen-plus 全部收割，提交 `81efce0`、`fb7e6a7`）；
- **对抗性超长 URL 遍历爆炸**（68KB 对抗 URL 曾达分钟级 CPU + 183MB 内存，zen-plus 修复至 ~1ms / ~0.7MB）。

---

### 安装 / Install

从 [Releases](https://github.com/wlfzsd/zen-plus/releases) 下载 `Zen.exe`（附 SHA256 校验值；未做代码签名，SmartScreen 提示属正常）。覆盖安装到 `%LOCALAPPDATA%\Programs\Zen\` 后启动即可，配置与过滤器缓存沿用原版。

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

`tmp_enginebench/` 是全部性能与正确性结论的复现装置（冻结基线对拍、黄金回放、并发压力、广告测试页数据集、真实订阅语料装载器），用法见 [tmp_enginebench/README.md](tmp_enginebench/README.md)。对官方 v0.26.0 的完整对比数据与测试方法见 [docs/benchmarks/v3.1.0-vs-upstream-v0.26.0.md](docs/benchmarks/v3.1.0-vs-upstream-v0.26.0.md)。

### License

MIT（保留上游 [LICENSE](LICENSE)）。上游项目与团队：[irbis-sh/zen-desktop](https://github.com/irbis-sh/zen-desktop) • [irbis.sh](https://irbis.sh/zen)。

---

<a id="english"></a>

## English

# zen-plus: faster, leaner, and far more capable than the official latest release

**zen-plus is an enhanced fork of [Zen](https://github.com/irbis-sh/zen-desktop)**. Measured on the same machine, the same rule set, the same real-world URL corpus: **the filter engine is 35% faster, per-request allocations are 73× smaller, resident memory is 10.5% lower — all while loading 9,490 MORE rules**. And it ships what the official latest release (v0.26.0) simply does not have: TLS fingerprint mirroring, HTTP/2 outbound replay, and an upstream proxy chain.

> Attribution: based on [irbis-sh/zen-desktop](https://github.com/irbis-sh/zen-desktop) (MIT License, Copyright (c) 2026 Irbis & Zen contributors). The complete upstream commit history is preserved on the `master` branch; local enhancements live on `upstream-chain`.

---

### zen-plus vs official v0.26.0 at a glance

Measured on the same machine, same 13 filter lists (545k rules loaded production-style), 35,358 real URLs per pass, median of 5 rounds, single-thread pinned. Zero failures across the entire blocking-decision test set.

| Item | Official v0.26.0 | **zen-plus v3.1.0** | Result |
|---|---|---|---|
| Engine matching time / request | 234.8 µs | **153.1 µs** | **34.8% faster** |
| Engine matching (XHR/fetch-type) | 282.4 µs | **177.2 µs** | **37.3% faster** |
| Heap allocations / request | 149.3 KB / 1,625 | **2.03 KB / 13** | **73× / 124× fewer** |
| Engine live memory (after load, post-GC) | 205.5 MB | **184.0 MB** | **-10.5%** |
| Rules actually loaded | 535,887 | **545,377** | **+9,490 rules** |
| Parse failures (silently dropped) | 11,867 | **2,377** | **9,490 fewer dropped** |
| CPU for identical traffic | 3.91 ms/req | **2.71 ms/req** | **31% less CPU** |
| TLS fingerprint consistency | ✗ none | **JA3 byte-identical to direct** | **missing upstream** |
| HTTP/2 outbound replay | ✗ | ✅ | **missing upstream** |
| Upstream proxy chain | ✗ | ✅ HTTP/HTTPS/SOCKS5 | **missing upstream** |
| Blocking coverage (obfusgated 359-domain test) | 604 hits / 302 domains | **610 / 305** | **superset, zero reversals** |

---

### ✊ The truth behind that "-10.5%": less memory AND 9,490 more rules

A 10.5% memory reduction sounds modest — **until you notice zen-plus achieved it while loading 9,490 MORE rules**.

The official v0.26.0 parser silently drops **11,867 rules** (parse failures are discarded without any error), while zen-plus drops only 2,377. The gap: EasyList **3,139 vs 7**, AdGuard Base **6,314 vs 1,394**, AdGuard Spyware **1,465 vs 327**, AdGuard CN **903 vs 609**. Among the rules upstream drops are `$denyallow` rules covering phone-vendor ad domains — in a real test, oppo ad domains (adx/ck/data.ads.oppomobile.com) are allowed by official and blocked by zen-plus, and zen-plus's blocking set is a **strict superset** of upstream's (all 604 upstream hits are blocked here, zero reversals).

**More rules, less memory, faster matching — all three at once. That is what rewriting the engine buys you.**

---

### 🛡️ Fingerprint consistency: this feature once saved the author's own accounts

**A real story: the author kept getting banned by 1688's risk control when browsing through the original proxy.** After repeated investigation the root cause was found — after MITM, the original re-emits outbound TLS with the Go standard library's ClientHello fingerprint, **completely mismatched with the browser's own fingerprint**; risk-control systems flag it instantly as "proxy traffic". It is an extremely stealthy failure (it just looks like "the proxy works but I keep getting banned") and an extremely costly one.

zen-plus's answer is **TLS fingerprint mirroring** (`internal/proxy/uptls.go`): re-emitted TLS connections mirror the browser's **own ClientHello structure** — **Chrome goes out as Chrome, Firefox goes out as Firefox; no profile is invented or selected**. Verified: JA3 through zen-plus is **byte-for-byte identical to a direct connection**. Since v3.1.0 it goes further: an HTTP/2 client is re-originated over HTTP/2 from the captured h2 fingerprint parameters (SETTINGS values and order, WINDOW_UPDATE, PRIORITY frames, pseudo-header and header order) in `internal/proxy/reoriginate.go`.

**The official v0.26.0 still has none of this** (verified at source level). If you browse through any proxy, "proxy fingerprint" risk control is a sword hanging over every session — zen-plus takes that sword away. Known limits (ALPS/ECH dropped, etc.) are documented in the source-file header notes.

---

### 🌏 Upstream proxy chain: one answer for censored networks

**The official release has no upstream proxy feature at all** (verified at source level). Want "system-wide ad blocking + your own proxy egress" together? Official can't; zen-plus does it natively:

- **HTTP / HTTPS / SOCKS5 upstreams, optional credentials**, configured in-app under Settings → Advanced;
- All proxied traffic is further forwarded through **your own Clash / sing-box / airport node** — a seamless overlay;
- **DNS stays at the upstream**, filter-list downloads chain too, and the setting mirrors into the standard proxy environment variables;
- Failed dials are retried with a fresh spec — **no silent fallback path**: it never quietly turns into a direct connection.

The measured cost is transparent: +2~6 ms on hot connections for domestic sites (within ±10 ms on most, imperceptible), ~14 ms on cold connects — in exchange for **ad blocking and your own egress in one app**, no more choosing between "system proxy" and "ad blocking".

---

### 🧩 AdGuard compatibility: built to AdGuard's official semantics

zen-plus implements AdGuard's official filter syntax semantics batch by batch: exception semantics (per-request cancellation, `$document` page-scope exemptions), `$badfilter` / `$removeparam` / `$replace` / `$csp` / `$permissions` / `$cookie`, full `$jsonprune` AdGuard-dialect preprocessing (unquoted unions, `?(has/…)` conditions, `[-]`/`{-}`/`\$..` extended paths), 12 `trusted-*` scriptlet keys, response-path `$xmlhttprequest` via Fetch Metadata, response-side `$domain` (180 dead rules revived), and more. Every batch was gated with per-line attribution over an 833k-line real-corpus diff (0 unattributed).

The result is the table above: **the ~9,490 rules upstream drops — most of them from AdGuard-family lists — all work in zen-plus**. And zen-plus is **system-wide**: not just the browser, every app's traffic passes the engine. What used to take an AdGuard browser extension plus a system-level solution, zen-plus does in one app — and eats more rules than either.

---

### 📌 Note: historical measurements against the older v0.25.1 baseline

The full rewrite payoff against official v0.26.0 is the table above. Against the older v0.25.1 baseline, end-to-end measurements were: matching -60.7%, allocations -93.4%, per-rule memory -21.5% (multi-round medians, details in [docs/benchmarks/](docs/benchmarks/)). zen-plus also fixed two problems that remain unfixed upstream (still absent in v0.26.0, verified at source level):

- **Memory doubling when toggling ad-blocking on/off** (upstream leaks connection servers / retired certificate generators / tunnels on every toggle; zen-plus reaps all of them, commits `81efce0`, `fb7e6a7`);
- **Traversal explosion on adversarial long URLs** (a 68KB adversarial URL cost minutes of CPU and 183MB of memory; fixed to ~1ms / ~0.7MB in zen-plus).

---

### Install

Grab `Zen.exe` from [Releases](https://github.com/wlfzsd/zen-plus/releases) (SHA256 attached; binaries are unsigned, so SmartScreen may warn). Replace `%LOCALAPPDATA%\Programs\Zen\Zen.exe` and start — configuration and filter caches are shared with upstream builds.

### Build from source

```bash
# Requires Go 1.27+ and Node.js 24+
wails build -o Zen.exe -platform windows/amd64 -tags prod
# Output: build/bin/Zen.exe
```

### Verification harness

`tmp_enginebench/` reproduces every performance and correctness claim (frozen-baseline A/B, golden replay, concurrency stress, ad-test-page datasets, real-subscription corpus loader) — see [tmp_enginebench/README.md](tmp_enginebench/README.md). Full data and methodology of the v0.26.0 comparison: [docs/benchmarks/v3.1.0-vs-upstream-v0.26.0.md](docs/benchmarks/v3.1.0-vs-upstream-v0.26.0.md).

### License

MIT (upstream [LICENSE](LICENSE) preserved). Upstream project and team: [irbis-sh/zen-desktop](https://github.com/irbis-sh/zen-desktop) • [irbis.sh](https://irbis.sh/zen).
