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

zen-plus 使用自己的版本线（当前 **3.0.0**，起点基于上游 v0.25.1）；每次合并上游后递增 3.x，对应的上游基线写进 Release 说明。exe 内的版本号由构建时的 git tag 自动注入。
zen-plus keeps its own version line (currently **3.0.0**, starting point based on upstream v0.25.1); each upstream merge bumps 3.x, and the upstream base is stated in every release's notes. The binary version string is injected from the git tag at build time.

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
