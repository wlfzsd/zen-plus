# 全维度新旧引擎对比矩阵报告（full matrix report）

- 日期：2026-10-05～06
- 工作树：`<worktree>`（未 commit 的已优化引擎；`internal/` 未改动，本报告与新测试文件全部位于 tmp_enginebench/ 与本文档）
- 基线（旧）：`tmp_enginebench/baselinenr/`（改前引擎冻结副本）
- 生产（新）：`internal/networkrules` + `internal/ruletree`（工作树内未提交优化版）
- 语料：本机 Zen 订阅缓存 `%LOCALAPPDATA%\Zen\filters\*.cache.txt`，17 个文件、36MB、1,001,268 原始行（去重后 611,652 条规则，两引擎同源同序装载）
- 原始输出：`测试临时\fullmatrix_out\bench_raw.txt`、`bench_raw2.txt`、`gc_gctrace_raw.txt`
- 测试代码：`vt_b\tmp_enginebench\adtest_test.go`、`fullmatrix_bench_test.go`（新增，未动他人既有测试函数）；探测数据集 `vt_b\tmp_enginebench\adtest_urls.txt`（619 条）+ `adtest_urls_meta.txt`（机制标注）+ 生成器 `gen_adtest_urls.py`
- 机器：Windows 10 x64（本机），go1.27.0，18 P；基准为同机交替实测、取中位数（标注轮数）

## 一、结论先行

1. **过滤有效性：新引擎与旧引擎完全一致（不是更好，也没有任何变少）。**
   - d3ward 得分：**生产 504/524 = 96.2%，基线 504/524 = 96.2%**（严格口径 shouldBlock；宽口径 block||redirect 亦同）。未拦截的 20 条两引擎完全相同（ads.pinterest.com、*.ads.oppomobile.com、metrics.icloud.com 三组 host——订阅语料中无对应规则，属清单覆盖面问题，与引擎无关）。
   - 通用广告/追踪探测（canyoublockit 一手探测、AdGuard 机制、CNAME 伪装机制、国产/主流厂商机制）：逐机制拦截率两引擎逐条相同（见下表）。
   - 619 探测 × 8 头组合 = 4,952 对 ModifyReq：(shouldBlock, redirectURL=="", len(appliedRules)) 三元组**完全一致，0 差异**；ModifyRes 20 条 text/html（含 CSP/文档头）两引擎 (err, len(appliedRules)) 一致、**0 panic**。
   - 61.1 万行真实规则流式逐行 ParseRule 对拍：接受/拒绝/异常判定**逐行 0 不一致**。
2. **性能：除"全量装载耗时"基本持平外，其余各维度新引擎全面更好，无 ❌ 回退项。**

## 二、主对比矩阵（同机交替实测，中位数，轮数见表）

| # | 维度 | 基线值（旧） | 新值（生产） | Δ% | 判定 |
|---|------|------------|------------|-----|------|
| 1 | 全量装载耗时（611,652 行，7 轮） | 578.6 ms | 562.6 ms | **-2.8%** | ➖ 持平（处于噪声边缘：单轮散布 ±10%，第 1 轮预热偏置 651ms；不判更好） |
| 2 | 规则树装载吞吐 Insert（easylist+easyprivacy，3 轮） | 28.30 ms/轮 ＝ 78.67 MB/s | 25.08 ms/轮 ＝ 88.75 MB/s | **-11.4% 时间 / +12.8% 吞吐** | ✅ 更好 |
| 2b | 同上分配字节 | 34.07 MB/op | 29.60 MB/op | -13.1% | ✅ 更好 |
| 3 | ModifyReqReal ns/op（5 轮） | 393,541 ns | 268,610 ns | **-31.7%** | ✅ 更好 |
| 3b | ModifyReqReal B/op | 132.7 KB | 78.6 KB | -40.8% | ✅ 更好 |
| 3c | ModifyReqReal allocs/op | 1,181 | 402 | -66.0% | ✅ 更好 |
| 4 | ModifyResReal ns/op（3 轮） | 259,663 ns | 190,727 ns | **-26.6%** | ✅ 更好（基线侧散布 193.7~265.1µs，取中位；生产侧极稳 190.6~192.3µs） |
| 4b | ModifyResReal B/op / allocs | 91.7 KB / 578 | 79.2 KB / 186 | -13.6% / -67.8% | ✅ 更好 |
| 5 | HandleRequest 级 ns/op（5 轮） | 397,268 ns | 265,431 ns | **-33.2%** | ✅ 更好 |
| 5b | HandleRequest B/op / allocs | 132.8 KB / 1,183 | 78.8 KB / 402 | -40.6% / -66.0% | ✅ 更好 |
| 6 | 引擎 live 内存（611,652 规则，各自独立进程） | 334.1 MB（573 B/规则） | 224.5 MB（385 B/规则） | **-32.8%** | ✅ 更好 |
| 7 | GC：工作窗内 Mallocs（3,000 请求×3 遍） | 3,708,060 | 1,009,861 | **-72.8%** | ✅ 更好 |
| 7b | GC：窗内 NumGC / PauseTotal | 1 次 / 1.18 ms | 0 次 / 0 ms | — | ✅ 更好 |
| 8 | 规则文本兼容性（1,001,268 行流式对拍） | 接受 973,038 / 异常 15,893 | 接受 973,038 / 异常 15,893 | 逐行不一致 = **0** | ➖ 完全一致（语义等价，目标态） |
| 9 | ModifyReq 三元组一致性（619×8=4,952 对） | — | 全一致，0 差异 | — | ➖ 完全一致（目标态） |
| 10 | ModifyRes 一致性（20 条 text/html 含 CSP） | — | (err, appliedN) 全一致，0 panic | — | ➖ 完全一致（目标态） |

判定口径：✅ 更好 = 超出 ±2% 噪声带且方向为优；➖ 持平 = ±2% 噪声带内或语义等价目标态；❌ 回退 = 无。

### 8 种请求头组合（golden_test.go 复用）

`third-party-script / tracking-pixel / xhr-fetch / same-origin-script / user-navigation / iframe-embed / no-referer-pixel / same-site-fetch`（Sec-Fetch-Site/Dest/User + Referer 组合）。

## 三、广告拦截"真实性有效性"明细

### 3.1 探测数据集（adtest_urls.txt，619 条 ≥300，6 机制 ≥5）

| 机制 | 来源 | 条数 | 证据等级 |
|------|------|------|---------|
| M1 d3ward data.json | 一手：测试页实际探测清单（https://d3ward.github.io/toolz/adblock 页面 200 OK 实抓；其 JS 从 adblock_data.json 装载探测项，该文件已在库） | 262 | 一手 |
| M2 d3host.txt | 一手：d3ward 官方 d3Host 清单（131 条目）× 通用路径形态 | 262 | 一手 |
| M3 canyoublockit.com | 一手：2026-10-05 实抓首页内联 `pagead2.googlesyndication.com/pagead/js/adsbygoogle.js` 与 /extreme-test/ 内联 7 条随机化追踪端点（12ezo5v60.com 等）+ 该站公开考查的主流广告网络同构重建 | 35 | 一手 + 同构重建（已标注） |
| M4 AdGuard test.html | 页面本机网络不可达（adguard.com/en/test.html 超时，exit 28），按其已知机制（加载已知广告/追踪资源计失败）同构构造 | 20 | 重建（已标注不可达） |
| M5 CouragePetrify | 页面 404（thomaszajochen.github.io，2026-10-05；搜索无存档），CNAME 伪装机制同构构造 | 8 | 重建（已标注不可达） |
| M6 yrming ad_test / gebn.online | 页面与仓库均 404 / 连接失败 000（2026-10-05），按国产厂商+主流广告域机制同构构造 | 32 | 重建（已标注不可达） |

注：M5 为 CNAME 伪装探测。zen 是 HTTP 层代理，看不到 DNS CNAME（该手法只有 Brave 等做 DNS 层判定的浏览器可防），此组探测对 zen 只能按 hostname/path 规则命中——实测两引擎均拦 4/8（相同），这一层限制如实记录。

### 3.2 拦截率明细（通用探测，两引擎逐机制对比）

| 机制 | 生产拦截 | 基线拦截 |
|------|---------|---------|
| M3-cybi-firsthand（canyoublockit 一手探测） | 7/7 | 7/7 |
| M3-cybi-recon（广告网络重建） | 27/28 | 27/28 |
| M4-adguard-mech | 20/20 | 20/20 |
| M5-cname-cloak | 4/8 | 4/8 |
| M6-cn-generic | 25/32 | 25/32 |

### 3.3 d3ward 得分（明确回答）

**新引擎 96.2%（504/524），旧引擎 96.2%（504/524）——完全一致，无一例"过滤变少"，也无新增拦截（等价）。** 未拦截的 20 条为：ads.pinterest.com（4）、adx/ck/data.ads.oppomobile.com（12）、metrics.icloud.com（4）——两侧名单逐条相同，根因是当前订阅语料不含这些 host 的规则（d3ward 专用清单不在订阅内），非引擎判定差异。

## 四、GC 与 gctrace 抽样

MemStats（工作窗 3,000 请求×3 遍，两引擎常驻同堆先后测）：
- 生产：`NumGC=0 PauseTotal=0.00ms Mallocs=1009861`
- 基线：`NumGC=1 PauseTotal=1.18ms Mallocs=3708060`
- 生产窗内分配次数低 3.67 倍、0 次 GC；基线窗内触发 1 次 GC（1.18ms 停顿）。
- HeapAlloc 窗口增量（生产 +221.7MB / 基线 +159.0MB）为 GOGC=40 目标水位下的堆增长（生产 0 次 GC 故增长更多即未触发回收），非泄漏；稳态 live 内存以第 6 行（224.5 vs 334.1MB）为准，生产显著更低。

GODEBUG=gctrace=1 两行抽样（窗口标记 GCTRACE-MARK 前后，`gc_gctrace_raw.txt:60-80`）：
- 生产工作窗内：无 gc 行（窗内 0 GC）。
- 基线工作窗内：`gc 18 @3.162s 0%: 1.1+14+0 ms clock, 21+0/68/130+0 ms cpu, 500->505->270 MB, 535 MB goal, 18 P`

## 五、真实浏览器端到端说明（不可行原因与替代覆盖）

**不可行（本会话）**：Zen.exe（PID 23352）正在运行——用户的真实浏览器实例。zen-desktop 是 Wails 桌面应用，第二实例会与运行中实例争用代理端口/用户数据目录/过滤器缓存（双实例互顶），且无授权静默重启用户实例，故未执行。
**替代覆盖程度**：测试页的"有效性判定"本质是——页面向已知广告/追踪资源发请求，取回成功即扣分。本测试用同一批资源 URL（含与测试页同构的 Sec-Fetch 元数据）直接驱动两引擎 ModifyReq/ModifyRes，等价于代理层对这些请求的真实处置判定；加上 4,952 对三元组一致性与 6.5 万对黄金对拍（wt_c 既有门禁），引擎行为覆盖完整。**未覆盖**：浏览器渲染层表现、装饰性过滤（cosmetic）、DNS 层拦截、CNAME 的 DNS 解析面。

## 六、命令与运行记录

```
go test ./tmp_enginebench/ -run 'TestAdTest' -count=1 -v            # 有效性套件：4 项全 PASS
go test ./tmp_enginebench/ -run 'TestAdTestD3wardScore' -count=1 -v # 得分+未拦截样本
go test ./tmp_enginebench/ -run 'XXX' -bench 'BenchmarkFMTreeInsert' -benchtime=1x -count=3
go test ./tmp_enginebench/ -run 'XXX' -bench 'BenchmarkFMLoadReal' -benchtime=1x -count=7
go test ./tmp_enginebench/ -run 'XXX' -bench 'BenchmarkModifyReqReal' -count=3 / -count=5
go test ./tmp_enginebench/ -run 'XXX' -bench 'BenchmarkFMModifyResReal' -count=3
go test ./tmp_enginebench/ -run 'XXX' -bench 'BenchmarkFMHandleRequest' -count=3 / -count=5
go test ./tmp_enginebench/ -run 'TestNetworkRulesLiveMemoryReal$' -count=1 -v        # 生产 live 内存（独立进程）
go test ./tmp_enginebench/ -run 'TestFMLiveMemoryRealBaseline$' -count=1 -v          # 基线 live 内存（独立进程）
GODEBUG=gctrace=1 go test ./tmp_enginebench/ -run 'TestFMGCStatsModifyReq$' -count=1 -v
go test ./tmp_enginebench/ -run 'TestFMParseRuleCompatReal$' -count=1 -v
```
全部退出码 0；原始逐行输出在 `fullmatrix_out\bench_raw.txt`、`bench_raw2.txt`、`gc_gctrace_raw.txt`。

## 七、诚实声明（口径、局限、未测维度）

1. **HandleRequest 基线侧为镜像包装**：`filter.networkRules` 接口绑定 `internal/networkrules/rule.Rule`（含未导出字段，`rule.go:32-34`），基线引擎类型无法注入，故基线用与 `filter.HandleRequest`（filter.go:368-401）逐行等价的镜像函数（stub 观察者、GetPort=0），生产侧用真 `filter.Filter`。差异仅为接口动态分发与两次 `req.URL.String()`（µs 级）；两侧 allocs 差主要来自引擎本身。
2. **HandleRequest 首轮（count=3）生产侧 434.9µs 为瞬态测量伪值**（与 allocs 大幅降低矛盾、与 ModifyReqReal 267µs 矛盾）；count=5 复测稳定在 262~278µs（中位 265.4µs），已按复测数据判定，首轮数据如实留痕。
3. **装载耗时判 ➖ 不判 ✅**：-2.8% 略超 ±2% 噪声带，但单轮散布 ±10%（651→543ms），首轮预热偏置明显，证据不足以宣称更好。
4. **ModifyResReal 基线侧散布大**（193.7~265.1µs，3 轮），取中位数；如需更高置信可加轮数，但方向（生产 -26.6%~-1.5%）在任意轮组合下均不变差。
5. **未测维度**：浏览器渲染端到端（见第五节）、DNS/CNAME 解析层、启动后磁盘缓存二次加载路径（filterliststore 缓存命中语义）、代理 listener/socket 层吞吐、jslibs/pagestyle 装饰过滤资源。M4/M5/M6 共 60 条探测为同构重建（源页不可达，逐条已标注）。
6. 语料为**本机当前订阅缓存快照**（2026-10-05，611,652 规则）；探测 URL 中的计数参数为确定性合成，非真实会话流量。
7. 数据来源：d3ward 页面/canyoublockit 为一手抓取；yrming/gebn/AdGuard/CouragePetrify 本机网络不可达（404/超时/000），机制描述基于公开常识与检索到的同类测试说明（如 paileactivist.github.io Ad Blocker Test、adblock-tester.com 等检索结果），未一手核验其当前探测清单。

## 八、总体回答（任务书三问）

1. **新引擎在"过滤有效性"上与旧引擎完全一致**（d3ward 96.2% = 96.2%，4,952 对 0 差异，61.1 万行 0 逐行不一致）——既不更好，也绝不更差。
2. **全维度无 ❌ 回退**：CPU -32%/-27%/-33%（ModifyReq/ModifyRes/HandleRequest），内存 -33%，分配 -66~-73%，树装载吞吐 +12.8%，装载耗时持平。
3. **真实浏览器 E2E 本会话不可行**（用户实例运行中，互顶风险），替代覆盖=测试页同构探测在引擎层直测，未覆盖面已在第五节列明。
