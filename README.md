<div align="center">

# zen-plus

**中文** | [English](#english)

</div>

---

## 中文

**zen-plus 是 [Zen](https://github.com/irbis-sh/zen-desktop) 的增强分支**：一款开源的系统级广告拦截与隐私防护应用。zen-plus 在上游基础上对**过滤规则匹配引擎**分阶段迭代：先以"语义零变化"为硬约束完成两轮深度性能优化（v3.0.0），再向 AdGuard 官方语义靠拢（兼容批次与响应路径修复，v3.1.0），随后完成健壮性与性能强化（遍历爆炸修复、覆盖去重、动作索引、热路径提速）。

> 上游归属：本项目基于 [irbis-sh/zen-desktop](https://github.com/irbis-sh/zen-desktop)（MIT License，Copyright (c) 2026 Irbis & Zen contributors）。全部上游提交历史完整保留在 `master` 分支，本地增强在 `upstream-chain` 分支。所有修改以 MIT 协议回馈社区。

### 引擎演进 / Engine evolution

引擎增强分三个阶段推进，每个阶段都带与改前引擎的逐请求对拍门禁：

#### 第一、二轮（v3.0.0）：语义零变化的性能优化

两轮均以"**语义零变化**"为硬约束——黄金回放 3.6 万×2 全一致、1 万真实 URL 等价 0 差异、20 万随机 URL 模糊 0 差异、32 线程并发压力 0 差异：

1. **正则形状特化**（`internal/networkrules/fastshape.go`）：对 `^https?://…` 等常见正则形状在解析期生成精确等价的快速匹配器，未识别形状回落原 regexp 引擎；
2. **最稀 token 反向索引**（`internal/networkrules/tokenindex.go`，借鉴 Ghostery adblocker 技巧）：对回落的正则按"必需字面量"建索引，URL 不含 token 的正则直接跳过（零漏报证明见 `docs/benchmarks/probe-report.md`）；
3. **请求级缓存**：Referer 主机名、有效 TLD+1、用户导航守卫等每请求只算一次；
4. **内存结构瘦身**（`internal/networkrules/rule`、`internal/ruletree`）：规则对象 4 个切片头 → 2 个惰性指针，树节点 96B → 64B；
5. **低分配遍历**：树遍历共享累加器、去重 map 与结果切片池化，每请求分配从 1175 次降至 225 次。

#### 第三轮（v3.1.0）：AdGuard 语义兼容

让拦截行为贴近 AdGuard 官方语义——自本轮起不再承诺"与上游逐请求一致"，每批语法变更以 83 万行真实语料逐行归因对拍（0 未归因）：

- **兼容批次 B1-B8**：例外语义重构（逐请求取消、`$document` 页面级豁免）、popup/$cookie/$script:inject 别名、`$badfilter`/`$removeparam`/`$replace`/`$csp`/`$permissions` 语法补齐；
- **`$jsonprune` 方言预处理**：未加引号联合、`?(has/…)` 条件映射为原生路径，附带扩展路径（`[-]`/`{-}`/`\$..`/`[?()]`/`[=]`）与 12 个 `trusted-*` 脚本注入键，激活一批此前解析失败或静默失效的规则（youtube/twitter/msn/pluto 等）；
- **响应路径判定修复**：`$xmlhttprequest` 改用 Fetch Metadata 判定（37 条死规则复活）；`$domain` 此前在响应侧恒不命中（180 条响应动作规则复活）；`$permissions` 按文档仅作用于 frame 加载（此前全部响应都被误加 Permissions-Policy 头）；`$document` 异常按动作/查询修饰符正确收窄（修复脚本注入全站失效的机制性回归）。

#### 第四轮（v3.1.0）：健壮性与性能强化

- **超长/组合 URL 遍历爆炸修复**：68KB 对抗 URL 从分钟级 CPU＋183MB 内存降至 ~1ms/~0.7MB，1017 字符 `??` 组合 URL 从 15s+ 降至 8.4ms（全状态记忆化＋尾通配吞并＋4096 字符截断，对齐 AdGuard 行为）；
- **同签名规则覆盖去重**：默认列表裁剪 14,581 条被覆盖规则，live 堆 -3.3MB，33,303 URL 决策 0 差异（`ZEN_RULE_COVER_DEDUP=0` 可回退）；
- **match-all `$cookie`/`$removeparam` 动作索引**：ModifyReq 352 → 241µs/op；
- **ModifyReq 热路径再提速 12.6%**（180.3k → 157.6k ns/req，黄金对拍 133,324 决策 0 差异）；兼容批次引入的分配回归同步压回（语料回放每请求 413 → 7 次：查询状态池化、例外三桶分类）；
- **hostmatch 自 TLD 向下建树**（同步上游 #810）：matcher 内存 38MB → 6.8MB，asset 引擎 retained heap 42MB → 9MB。

### 相比上游的修复与增强 / Fixes & enhancements beyond upstream

- **修复：过滤开关反复切换导致内存大幅增长**（上游 bug）。上游在禁用/启用规则拦截时，内层连接服务器、退休的证书生成器与双向隧道不被回收，内存逐次翻倍；zen-plus 在 Stop 路径全部收割（提交 `81efce0`、`fb7e6a7`），并为传输空闲池设上限、空闲内存定期归还 OS。
- **新增：上游代理链**（支持 HTTP / HTTPS / SOCKS5 上游，可带凭证）。在应用内 设置 → 高级 中直接配置（存入 config.json，旧 `upstream-proxy.txt` 首次启动自动迁移），全部代理流量再经上游转发，用于叠加自有网络出口；目标主机名不在本地解析——DNS 留在上游，设置还会镜像到标准代理环境变量，过滤列表下载同样走链。镜像架构：拨号失败按新规格重拨镜像重试，**无任何静默降级路径**。
- **新增：TLS 指纹镜像与出站协议重放**（`internal/proxy/uptls.go`、`internal/proxy/reoriginate.go`）。MITM 后重新出站的 TLS 连接镜像浏览器自己的 ClientHello 结构指纹（uTLS）：Chrome 出去就是 Chrome 指纹、Firefox 就是 Firefox——不指定、不伪造任何档案。实测同一请求经 Zen 与直连**出口 JA3 逐字节一致**，规避"代理 TLS 指纹错配"类网站封控。v3.1.0 起出站协议跟随客户端：HTTP/2 客户端按捕获的 h2 指纹参数（SETTINGS 值与顺序、连接级 WINDOW_UPDATE、PRIORITY 帧、伪头与头部顺序）在 HTTP/2 上重放，HTTP/1.1 客户端保持 HTTP/1.1。已知边界：ALPS/ECH 扩展剔除、后量子混合密钥交换组被裁（Go 无法生成对应 key share）、h2 重放不含 PRIORITY_UPDATE 且不控制 HPACK 索引表示（详见源码头注）。

### 实测效果 / Measured results（真实订阅语料 61.1 万条规则，多轮中位数）

| 指标 Metric | 上游基线 Upstream | zen-plus | 变化 Δ |
|---|---|---|---|
| 引擎匹配耗时 Matching time / request | 370,336 ns | **145,651 ns** | **-60.7%** |
| 每请求堆分配 Allocations / request | 1,175 次 / 132 KB | **225 次 / 8.7 KB** | **-80.9% / -93.4%** |
| 引擎 live 内存 Engine live memory | 494 B/规则 rule | **388 B/规则** | **-21.5%** |
| 过滤列表装载 List loading | 560 ms | 521 ms | -6.9% |
| 拦截有效性（d3ward 测试页得分） | 96.2% | **96.2%** | 完全一致 identical |

> 注：上表为第一、二轮（v3.0.0）相对上游基线的端到端实测。第三、四轮（v3.1.0）的增量在引擎级另有实测，口径不同、不可与上表直接相加——明细见各 Release 说明与提交记录（如 ModifyReq 热路径再 -12.6%、覆盖去重 live 堆 -3.3MB 且决策 0 差异）。

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

**zen-plus is an enhanced fork of [Zen](https://github.com/irbis-sh/zen-desktop)**, an open-source system-wide ad-blocker and privacy guard. On top of upstream, zen-plus evolves the **filter-rule matching engine** in phases: two rounds of deep performance optimization under a zero-semantic-change constraint (v3.0.0), then AdGuard-semantics compatibility (compatibility batches and response-path fixes, v3.1.0), then robustness and performance hardening (traversal-explosion fixes, coverage dedup, an action index, hot-path tuning).

> Attribution: based on [irbis-sh/zen-desktop](https://github.com/irbis-sh/zen-desktop) (MIT License, Copyright (c) 2026 Irbis & Zen contributors). The complete upstream commit history is preserved on the `master` branch; local enhancements live on `upstream-chain`.

### Engine evolution

The engine work advanced in three phases, each gated by request-by-request replay against the previous engine:

#### Rounds 1 & 2 (v3.0.0): performance under zero semantic change

Both rounds are hard-constrained by **zero semantic change** — golden replay: 36,231×2 exact matches; 10k real-URL equivalence: 0 diffs; 200k random-URL fuzz: 0 diffs; 32-thread stress: 0 diffs:

1. **Regexp shape specialization** (`internal/networkrules/fastshape.go`): common shapes like `^https?://…` are compiled at parse time into exact-equivalent fast matchers; unrecognized shapes fall back to `regexp.Regexp`;
2. **Rarest-token reverse index** (`internal/networkrules/tokenindex.go`, technique credited to the Ghostery adblocker): fallback regexps are indexed by their provably-required literals; a URL missing the token skips the regexp entirely (zero false negatives — proof in `docs/benchmarks/probe-report.md`);
3. **Per-request caches**: referer hostname, effective TLD+1, and the user-navigation guard are computed once per request instead of once per candidate rule;
4. **Memory layout slimming** (`internal/networkrules/rule`, `internal/ruletree`): rule structs went from 4 slice headers to 2 lazy pointers; tree nodes from 96B to 64B;
5. **Low-allocation traversal**: shared traversal accumulator plus pooled dedup maps and result slices — per-request allocations dropped from 1,175 to 225.

#### Round 3 (v3.1.0): AdGuard semantics compatibility

Blocking behavior now follows the official AdGuard semantics — the "request-by-request identical to upstream" statement applied to v3.0.0 and no longer applies from v3.1.0 on; every syntax batch was verified with per-line attribution over an 833k-line real-corpus diff gate (0 unattributed):

- **Compatibility batches B1–B8**: reworked exception semantics (per-request cancellation, `$document` page-scope exemptions), popup/$cookie/$script:inject aliases, and `$badfilter`/`$removeparam`/`$replace`/`$csp`/`$permissions` syntax coverage;
- **`$jsonprune` dialect preprocessing**: unquoted unions and `?(has/…)` conditions mapped to native paths, plus extended paths (`[-]`/`{-}`/`\$..`/`[?()]`/`[=]`) and 12 `trusted-*` scriptlet keys — activating rules that previously failed to parse or were silently inert (youtube/twitter/msn/pluto and others);
- **Response-path condition fixes**: `$xmlhttprequest` now decided via Fetch Metadata (37 dead rules revived); `$domain` never matched on the response path before (180 response-action rules revived); `$permissions` now applies to frame loads only per the docs (every response used to get the Permissions-Policy header); `$document` exceptions are correctly narrowed by action/query modifiers (fixing a mechanism where scriptlet injection was disabled site-wide).

#### Round 4 (v3.1.0): robustness and performance hardening

- **Traversal-explosion fix for very long / combinatorial URLs**: a 68KB adversarial URL went from minutes of CPU and 183MB of memory to ~1ms / ~0.7MB; a 1017-char `??` combo URL from 15s+ to 8.4ms (full-state memoization + tail-subsumption + 4096-char truncation, matching AdGuard);
- **Same-signature coverage dedup**: 14,581 covered rules pruned on the default lists, 3.3MB live heap, zero decision diffs over 33,303 URLs (`ZEN_RULE_COVER_DEDUP=0` rolls back);
- **Match-all `$cookie`/`$removeparam` action index**: ModifyReq 352 → 241µs/op;
- **ModifyReq hot path another 12.6% faster** (180.3k → 157.6k ns/req, golden master 133,324 decisions, 0 diffs); the allocation regression introduced by the compatibility batches was pushed back down (corpus replay 413 → 7 per request: query-state pooling, three-bucket exception classification);
- **hostmatch trie keyed from the TLD down** (upstream #810 synced): matcher memory 38MB → 6.8MB, asset-engine retained heap 42MB → 9MB.

### Fixes & enhancements beyond upstream

- **Fixed: large memory growth when toggling ad-blocking on/off** (upstream bug). Upstream leaks per-connection servers, retired certificate generators and bidirectional tunnels across filter toggles; zen-plus reaps all of them on Stop (commits `81efce0`, `fb7e6a7`), caps transport idle pools and returns idle memory to the OS periodically.
- **Added: upstream proxy chain** (HTTP, HTTPS and SOCKS5 upstreams, optional credentials). Configure it in the app under Settings → Advanced (stored in config.json; a legacy `upstream-proxy.txt` is imported once on first start) to route all proxied traffic through an upstream of your choice; target hostnames are left unresolved locally — DNS stays at the upstream — and the setting is mirrored into the standard proxy environment variables so filter-list downloads chain too. Mirror architecture retries dialing with a fresh spec — **no silent fallback path**.
- **Added: TLS fingerprint mirroring and outbound protocol replay** (`internal/proxy/uptls.go`, `internal/proxy/reoriginate.go`). Re-emitted TLS connections after MITM mirror the browser's **own ClientHello structural fingerprint** (uTLS): Chrome goes out as Chrome, Firefox as Firefox — no profile is invented or selected. Verified: JA3 through Zen is **identical, byte-for-byte, to a direct connection**, defusing the "proxy TLS fingerprint mismatch" class of site blocks. Since v3.1.0 the outbound protocol follows the client: an HTTP/2 client is re-originated over HTTP/2 from the captured h2 fingerprint parameters (SETTINGS values and order, connection-level WINDOW_UPDATE, PRIORITY frames, pseudo-header and header order); an HTTP/1.1 client stays on HTTP/1.1. Known limits: ALPS/ECH extensions dropped, post-quantum key-share groups dropped (Go cannot generate the corresponding shares), h2 replay emits no PRIORITY_UPDATE and no HPACK index-representation control (see the source-file header notes).

### Measured results (real-subscription corpus, 611,652 rules, medians)

| Metric | Upstream baseline | zen-plus | Δ |
|---|---|---|---|
| Engine matching time / request | 370,336 ns | **145,651 ns** | **-60.7%** |
| Heap allocations / request | 1,175 / 132 KB | **225 / 8.7 KB** | **-80.9% / -93.4%** |
| Engine live memory | 494 B/rule | **388 B/rule** | **-21.5%** |
| Filter-list loading | 560 ms | 521 ms | -6.9% |
| Blocking effectiveness (d3ward test page) | 96.2% | **96.2%** | identical |

> Note: the table measures rounds 1 & 2 (v3.0.0) against the upstream baseline, end to end. Rounds 3 & 4 (v3.1.0) were measured at engine level with different methodologies — the numbers are not additive with this table; see the release notes and the individual commits (e.g. ModifyReq hot path another -12.6%, coverage dedup 3.3MB live heap with zero decision diffs).

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
