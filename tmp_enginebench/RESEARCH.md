# 引擎技巧调研：Brave adblock-rust / AdGuard TSUrlFilter / Ghostery adblocker

问题：在"正则形状特化 + 规则结构瘦身"（已立项）之外，是否还有值得 Go 移植、能再省 ≥10% CPU 或内存的技巧？

**结论（一句话）**：值得单独立项的只有两条——**token 反向索引（Ghostery"最稀 token"变体）替代 regexp 存储线性扫描** 与 **匹配 URL 的零分配构造**；bloom filter 在本引擎无适用点，FlatBuffers 与已立项的瘦身重叠，懒编译 regex 与插入期校验语义冲突。

## 逐条评估

| # | 技巧 | 来源引擎（一手证据） | 预期收益证据 | 移植成本/风险 |
|---|------|----------------------|--------------|---------------|
| 1 | **Token 反向索引 + 最稀 token 选择**：每条 filter 只按全局出现次数最少的 token 索引一次，请求按 URL token 查桶 | Ghostery adblocker `src/engine/reverse-index.ts:50-92`（注释明确"pick the most discriminative token"）；同思想在 Brave adblock-rust `src/network_filter_list.rs`（filter_map 按 `to_short_hash(token)` 查 + `opt_domains_map` 给 \|\| 锚定规则单列） | 本仓库原型（`bench_test.go` reStore trigram 预过滤，同思想简化版）在 easylist+easyprivacy 语料实测：线性扫描 8190ns → 预过滤 7330ns（**组件级 -10.5%**，20x bench，Ultra 5 250K）；**regexp 存储占 ModifyReq 总 CPU 77%**（`测试临时/modifyreq_cpu.out` 剖面结论），故组件级 -10.5% ≈ 全引擎 -8%，最稀 token 版桶更小预计可过 10% 总线（**未实测**） | 成本中（插入/去重簿记 + 退化桶处理）；风险中低：只影响存储内部组织，不改匹配语义；黄金对拍可直接守护 |
| 2 | **匹配 URL 零分配构造**：不重序列化 URL，直接拼 scheme://host+path+query | adblock-rust 自带 url_parser（`src/url_parser/`）；TSUrlFilter 的 Request 也直取 hostname/path | zen 每次 ModifyReq 调 `renderURLWithoutPort`+`URL.String()` 两次（`networkrules.go:25,78`；microbench `BenchmarkMicroRenderURLWithoutPort` 已存在）；真实引擎单请求 576µs/154KB 中 URL 重序列化占可测比例 | 成本低；风险低但**必须与现输出逐字节一致**（黄金对拍守护）；Referer 解析同理可只取 hostname |
| 3 | SIMD 子串搜索 | adblock-rust 依赖 memchr 2.8（Cargo.toml） | — | **无需移植**：Go `strings.Index`/`IndexByte` 在 amd64 已是 SIMD 汇编实现，天然具备 |
| 4 | Bloom 预过滤 | adblock-rust 的 bloom 用于 badfilter 集合判定 | zen 不支持 `$badfilter` → **无适用点**；等价物即 #1 的 token/字面量预过滤 | 不适用（诚实结论） |
| 5 | FlatBuffers/紧凑序列化 | adblock-rust `src/flatbuffers/`；Brave 应用实测省 ~45MB（shivankaul.com 博客，2026-01，**检索来源非一手，本机无法直连验证**） | 与"规则结构瘦身"目标重叠（本语料 611,652 条规则 295MB live，506 B/rule，已在优化射程内） | 单独立项成本高；不建议重复立项 |
| 6 | 懒编译 regex + LRU 缓存 | adblock-rust `src/regex_manager.rs` | 省"从不匹配的正则"的编译内存/装载时间 | **需绕开**：zen 在 Insert 期 `regexp.Compile` 即校验（`rulestore.go:40`），懒编译会改变"插入期拒绝"语义，违反黄金对拍门禁 |
| 7 | domain 分桶 | TSUrlFilter `lookup-tables/domains-lookup-table.ts`（domain hash → rule 索引） | zen 的 `$domain` 在 Get 之后逐条评估，建第二维索引需重排匹配架构 | 工程量大；≥10% 收益无证据，不推荐 |
| 8 | hostname 精确表 | TSUrlFilter `hostname-lookup-table.ts`（无路径规则先走 hostname 桶） | 上游曾做过同类（#7151dd9 "perf: store hosts in a separate map"），后被 radix 树统一（#484）；重加会双份存储 | 收益不明，历史已被上游否定，不推荐 |

## 附：本语料组件级实测（tmp_enginebench，20x）

- BenchmarkReStorePlain（regexp 线性扫描）：8190 ns/op
- BenchmarkReStorePrefiltered（trigram 必需字面量预过滤）：7330 ns/op（-10.5%）
- BenchmarkReStoreFast（形状特化，**属已立项范围**）：3745 ns/op（-54%）
- 全引擎 ModifyReq（17 订阅真实语料 611k 规则）：576,300 ns/op、154,820 B/op（BenchmarkModifyReqReal）

## 引用（一手源码，HEAD 快照 2026-10-05）

- ghostery/adblocker：`packages/adblocker/src/engine/reverse-index.ts`、`src/engine/bucket/network.ts`
- Brave/adblock-rust：`src/network_filter_list.rs`、`src/blocker.rs`、`src/filters/network.rs`（get_tokens/OptDomains）、`Cargo.toml`
- AdguardTeam/tsurlfilter：`packages/tsurlfilter/src/engine/network-engine.ts:107-110,142-145`（四表级联：hostname→shortcuts trie→domains→seq-scan）
- 检索来源（未一手核验）：shivankaul.com《Using FlatBuffers to reduce memory usage in adblock-rust》
