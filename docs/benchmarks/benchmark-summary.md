# zen-plus 性能验证汇总 / Performance Verification Summary

> 全部数据来自真实订阅语料（本机 17 个订阅缓存，去重后 **611,652 条规则**），
> 同机、同参、与冻结基线引擎交替实测取多轮中位数。
> All numbers were measured on a real-subscription corpus (611,652 deduplicated
> rules from 17 caches), same machine, same parameters, interleaved A/B against
> a frozen pre-optimization engine snapshot, medians of multiple runs.

## 一、拦截有效性（最重要：不能因优化而漏过滤）/ Blocking effectiveness

| 判定项 Check | 上游基线 Upstream | zen-plus | 结论 Verdict |
|---|---|---|---|
| d3ward 广告拦截测试页得分 d3ward score | 96.2% (504/524) | **96.2% (504/524)** | 完全一致 identical |
| 广告测试页探测 URL×8 种请求头（619 条×6 机制）三元组 | — | 4,952 对 0 差异 0 diff | 完全一致 identical |
| 真实流量 URL 结果集等价（10,000 条） | — | 0 差异 0 diff | 完全一致 identical |
| 黄金回放 ModifyReq+ModifyRes（36,231×2） | — | 0 差异 0 diff | 完全一致 identical |
| 随机模糊 URL（200,000 条） | — | 0 差异 0 diff | 完全一致 identical |
| 规则文本接受性（1,048,595 行逐行） | — | 0 不一致 0 diff | 完全一致 identical |
| 恶劣/畸形输入（72 项：1MB 行、NUL、非法 UTF-8、灾难回溯…） | — | 双引擎逐项一致、0 panic | 完全一致 identical |
| 并发压力（32 线程×100,000 混合请求） | — | 0 差异 0 panic | 完全一致 identical |
| 环境矩阵（GOMAXPROCS 1/2/默认 × GOGC off/10/400 × GOMEMLIMIT） | — | 行为签名恒定 | 完全一致 identical |

## 二、执行效率 / Execution efficiency

| 指标 Metric | 上游基线 Upstream | zen-plus | Δ |
|---|---|---|---|
| ModifyReq 引擎匹配 / request | 370,336 ns | **145,651 ns** | **-60.7%** |
| ModifyRes 响应过滤 / request | 259,700 ns | **190,700 ns** | **-26.6%** |
| HandleRequest 端到端（含拦截响应构造） | 397,300 ns | **265,400 ns** | **-33.2%** |
| 每请求堆分配次数 Heap allocs / request | 1,175 次 | **225 次** | **-80.9%** |
| 每请求堆分配字节 Heap bytes / request | 132,396 B | **8,699 B** | **-93.4%** |
| 规则树装载吞吐 Tree insert throughput | 85.1 MB/s | **90.4 MB/s** | **+6.2%** |

## 三、内存 / Memory

| 指标 Metric | 上游基线 Upstream | zen-plus | Δ |
|---|---|---|---|
| 引擎 live 内存（61.1 万规则） Engine live heap | 494 B/规则（288.4 MB） | **388 B/规则（226.2 MB）** | **-21.5%（-62 MB）** |
| GC 窗口内分配对象 Mallocs in bench window | 3,708,060 | **1,009,861** | **-72.8%** |
| 进程级验证 Process-level（新 exe 启动 3 分钟 vs 旧版 26h 挂机平台） | PM 510.7 MB | **PM 452.4 MB** | **-58 MB** |

## 四、优化手段 / What changed

1. 正则形状特化（`fastshape.go`）／Regexp shape specialization
2. 最稀 token 反向索引（`tokenindex.go`，技巧来自 Ghostery adblocker）／Rarest-token reverse index
3. 请求级缓存：referer 主机名、eTLD+1、用户导航守卫／Per-request caches
4. 规则结构与树节点瘦身（4 切片头→2 懒指针；节点 96B→64B）／Struct & node slimming
5. 低分配遍历：共享累加器 + 池化去重 map/结果切片 + 原地过滤／Low-alloc traversal with pooling

## 五、方法与边界 / Method & caveats

- 等价性判据＝多重集/三元组逐项一致，错配=0 才算通过；证据链见各报告。
- `-race` 竞态检测因构建机无 C 编译器未运行，以 18 核并发逐请求比对替代（已如实标注）。
- 浏览器渲染端到端未自动化（桌面应用），以测试页同构探测 URL 在引擎层直测替代。
- 长进程 RSS 曲线由小时级采样器持续记录（`memory-optimization-evaluation.md` 方法）。
- 语料、URL 集、请求头组合的构成见 `full-matrix-report.md` 与 `tmp_enginebench/`。

## 六、报告索引 / Report index

| 报告 Report | 内容 Contents |
|---|---|
| [engine-optimization-research.md](engine-optimization-research.md) | 引擎瓶颈定位与两轮优化立项依据 / Bottleneck profiling & optimization rationale |
| [stress-matrix-report.md](stress-matrix-report.md) | 恶劣输入与并发/环境压力矩阵 / Adversarial & stress matrix |
| [full-matrix-report.md](full-matrix-report.md) | 拦截有效性 + 全维度新旧对比 / Effectiveness & full-dimension A/B |
| [probe-report.md](probe-report.md) | 替代方案探针（token 索引/池化）实测与取舍 / Alternative-approach probes |
| [memory-optimization-evaluation.md](memory-optimization-evaluation.md) | 进程级内存方案穷举评估（进程层） / Process-level memory evaluation |
| [startup-memory-analysis.md](startup-memory-analysis.md) | 启动内存与挂机增长深度分析 / Startup & idle-growth analysis |
