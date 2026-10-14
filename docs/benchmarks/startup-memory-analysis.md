# 启动内存与挂机增长 · 深度分析报告

日期：2026-10-04 ｜ 方法：源码穷举走查 ＋ 真实数据实测（MemStats/heap profile）＋ 26 小时生产曲线
合规：仅诊断，未改任何代码（遵循用户暂停令）

---

## 方向Ⓐ 启动内存偏大——是 Go 语言的问题吗？

### 实测装置
在 `internal/filter` 写临时测试（已归档至 `测试临时/memharness_test.go.txt`），用**真实的 17 个订阅缓存文件**走与生产完全相同的解析路径（`AddReader→addRule→injector/networkRules`），逐列表 GC 后读 MemStats，最后写堆剖面。运行于 go1.27，GOGC=100 环境。

### 实测结果（业务闭环级证据）

| 指标 | 数值 | 含义 |
|------|------|------|
| Go 运行时基线（未加载规则） | **HeapAlloc 0.6MB / Sys 9.3MB** | Go 语言本身的开销几乎为零 |
| 规则数据 live（Finalize 后） | **249.7MB** | 54.5 万唯一网络规则 + 5.2 万外观规则的树结构 |
| 规则去重哈希集合 | 38.1MB | 加载期临时，Finalize 释放（filter.go:409-412），不占常驻 |
| 加载期累计分配 churn | 570.4MB | 瞬时垃圾——旧版 GOGC=100 启动冲高的直接原因 |
| 加载期堆峰值 | 288.3MB | 与生产日间形态吻合 |

### 堆剖面归因（99.05% 可解释，263.6MB 总量）

| 归属 | 占比 | 说明 |
|------|------|------|
| ruletree.Insert | 30.2% | 网络规则树的节点（radix 树，官方已优化过 #466/#484） |
| NetworkRules.ParseRule | 27.3% | 规则对象/修饰符 |
| hostmatch findOrAddChild | 12.2% | 外观规则域名 trie |
| Scanner.Text | 7.4% | 规则行字符串本身（95.6 万行文本） |
| hostmatch Add + trimslice + 修饰符解析等 | ~20% | 其余结构 |

### 定性结论

**不是 Go 语言的问题。** Go 运行时基线 <1MB（live），可忽略。启动内存 ≈ 250MB 全部是**过滤规则数据的物理成本**——这是"全系统广告拦截"的本质开销（uBlock Origin 在浏览器里持有同量级规则集，占的是浏览器内存；Zen 占自己的）。单列表实测增量也印证：AdGuard Spyware 一个文件 +147.6MB live（145.2→282.8），EasyList 只 +8.8MB（规则已被 AdGuard Base 去重共享——#804 生效的直接证据）。

生产进程与 harness 的差值（~250MB → 生产推算 live ~310-360MB）来自：连接池（最多 512 条 × ~40KB 缓冲）、证书 LRU（≤5800 张）、每连接 goroutine 栈与 TLS 缓冲、GC 元数据（≈堆的 4-8%）、Wails 桥。均为有界、随流量波动的正常项。

**可压部分只剩两处**：① GC 包络（live 之上的放大系数）——已从 2.0×(GOGC=100 默认) 压到 1.1×(GOGC=10)，见已实施的 d3e1085；② live 本身——只能删订阅（见主报告 B1，AdGuard Spyware 一张 = -147.6MB live，实测数）。

---

## 方向Ⓑ 长时间挂机内存持续增长——源码里有没有真实机制？

对空闲期所有仍在运行的代码路径做了穷举走查，逐一定性：

| # | 空闲期活跃路径 | 结论 | 证据锚点 |
|---|--------------|------|---------|
| 1 | **列表刷新是否叠加规则** | **无此机制**：规则库只在 StartProxy 构建一次（app.go:210→buildFilter→populateFilter），空闲期无任何代码调用 AddURL/ParseRule；列表 TTL 到期只影响磁盘缓存的重新拉取，新规则要等下次代理重启才进内存 | grep 全 app 包：populateFilter 仅 app.go:210 一处调用 |
| 2 | **Wails 事件队列积压**（前端隐藏/被节流时不消费） | **无 Go 侧队列**：Events.Emit→Notify→JSON 序列化→主线程消息泵→WebView2 Eval（异步、不等待渲染器）；排队只发生在 msedgewebview2 独立进程的内存里，不占 Zen.exe | wails v2.14.0 events.go:56-61、frontend.go:622-633/946-950（模块缓存源码） |
| 3 | 自更新调度器 | **不运行**：NoSelfUpdate=true 时 NewSelfUpdater 返回 nil（selfupdate.go:64-67），commonStartup 的 `su != nil` 守卫使其永不启动 | selfupdate.go:63-67 |
| 4 | 透明隧道（CONNECT/ws）无空闲超时 | 机制存在但**有界**：双向 copy 在两端任一 EOF/错误时退出（isCloseable，proxy.go:634-645），拨号侧 KeepAlive 30s；26h 数据中连接数/句柄随流量波动且回落（17:21 句柄 1654 → 18:21 回落 1586），非泄漏形态 | proxy.go:544-545/597-611 |
| 5 | 证书生成缓存 | 有界：LRU maxSize=5800 + 5 分钟 TTL 清理 + 24h 过期（certgen.go:15-17、lru.go:39-50） | certgen/lru.go |
| 6 | 连接池 | 有界：全局 512 上限 + 30s/90s 空闲回收，空闲期自行排空 | proxy.go:30-31、upstreamchain.go:195-201 |
| 7 | transparentHosts | 已去重；26h 仅 14 条追加 | proxy.go:535-550（本分支） |
| 8 | inflight/磁盘缓存/日志等 | 均有界：inflight 配对 delete（filterliststore.go:273/280）、日志 lumberjack 轮转、磁盘缓存条目=文件数 | 各文件 |
| 9 | GC 包络（非泄漏） | 唯一真实的"波动"来源：heap 在 live×1.1（现 GOGC=10）内随请求分配起伏；每 10 分钟 FreeOSMemory 把闲置部分还给 OS | main.go（本分支 d3e1085） |

### 生产数据交叉验证（26 小时，小时采样）

- 夜间空闲（02:21-08:21，六小时）：PM **532-536MB 纹丝不动**（GOGC=40 参数下）——若存在任何持续增长机制，六小时必然现形。
- 句柄 1654→1586 回落、线程稳定 73、连接数随流量 250-527 波动后回落——全部呈"流量相关震荡"而非"单调累积"。
- 挂机增长用户观察 = GC 包络的正常起伏（±30-50MB）被误读为增长；叠加 GOGC=100 旧参数时代的真实历史棘轮（已修复）。

### 定性结论

**源码中不存在真实的挂机增长机制。** 四个候选（刷新叠加/事件队列/自更新/隧道泄漏）全部被源码与数据双重排除；剩余波动全部属于 GC 包络与流量包络，且新版参数（GOGC=10 + 10 分钟归还）已把两者压到最低。若未来 CSV 出现"单调上涨越 1GB 且全天无回落"，届时用 pprof（需临时构建加端点）实锤——这是唯一可能推翻本结论的路径。

---

## 证据文件

- 实测原始输出：本报告上方命令记录（BASELINE/LOADED 17 行/PEAK/FINAL/DEDUP）
- 堆剖面：`测试临时/memharness_heap.pb.gz`（可用 `go tool pprof -top` 随时复查）
- 诊断装置归档：`测试临时/memharness_test.go.txt`（恢复方法：拷回 `zen-desktop/internal/filter/memharness_test.go` 后 `MEMHARNESS=1 MEMHARNESS_FILTERS=<filters目录> go test ./internal/filter/ -run TestStartupMemoryHarness -v`）
- 生产曲线：`logs/zen_mem_watch.csv`


---

## 附章（2026-10-04）：规则加载源码还有无『大幅降内存+提速+更稳』的明显优化点？

### 结论：没有满足三重标准的明显优化点——不值得动

**查询速度轴（官方基准实测，ruletree_benchmark_test.go）**：
- BenchmarkMatch 单线程 3034ns/次（57MB/s）；并行 225.3ns/次 = **767MB/s 匹配吞吐**，每次仅 6 次分配/196B
- 对比：一次代理请求的网络往返 10-500ms——匹配成本是网络延迟的十万分之一，已处噪声级，无可感知提升空间
- 请求路径 ModifyReq（networkrules.go:24-38）：树遍历 O(URL长度)+只过滤命中规则，无全量扫描

**内存轴**：live 249.7MB ≈ 458B/规则。树结构已是优化形态（排序边切片+二分 node.go:29/49、路径压缩 node.go:21、Compact 裁剪）——官方为此投入过 4 个带基准的优化 PR（#458/#466/#484/#467），低垂果实已摘完。现架构内剩余可挤项（hostmatch cosmetic trie 的 map→切片 ~10-15MB、修饰符驻留 ~15-25MB、字符串驻留 ~20-30MB）合计约 50-75MB，**只占进程总量（~425MB）的 12-18%**，且每一项都动匹配语义、有回归风险——不满足『明显』标准。

**要『大幅』只剩引擎级重写**（紧凑二进制序列化，理论 458B/规则→50-100B/规则，可省 150-200MB），但这是重写核心匹配层：数周工程+全部修饰符语义迁移（$all/$important/$domain/$removeparam/自研 removejsconstant/脚本信任），稳定性必然先降后升——与『增加稳定性』『不要产生新 bug』直接矛盾。AdGuard urlfilter 是现成参考实现，但 Zen 的自定义修饰符无法直接迁移。

### 判决
按用户标准（『如果不是明显的优化就算了』）：**就算了。** 本轮已实施的 #804 去重、GC 包络收紧、连接池上限就是全部的『明显』项；剩余项均为微优化+回归风险，或需引擎重写。留给上游社区做（若未来上游发布紧凑存储版本，届时同步即可白拿）。
