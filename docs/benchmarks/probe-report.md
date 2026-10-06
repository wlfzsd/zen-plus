# 引擎探针报告：正则反向索引 / 分配热点削减 / fastshape 审视 / 节点复核

工作树：`<worktree>`（zen-desktop，已优化未 commit 引擎之上）。日期：2026-10-05/06。
产出形态：**探针与提案**——internal/ 生产代码零改动（git status 中 internal/ 的 M 状态全部是本会话开始前已存在的优化基线），全部探针代码位于 `vt_c\tmp_enginebench\`（nrprobe/、ruletreelp/），可复跑。

## 0. 环境与方法

- 机器：Intel Core Ultra 5 250K Plus（18 线程），Go 1.27.0 windows/amd64。基准期间 CPU 负载 16–24%（其余为后台），每次 A/B 均为**同一二进制内切开关**的对比（规避跨二进制代码布局噪声，实测该噪声约 ±4%）。
- 语料：本机 17 个真实订阅缓存（%LOCALAPPDATA%\Zen\filters，37.6MB，去重后 611,652 行）；URL 集：urls.txt 前 3000/5000 条。
- 方法：每配置 5 轮（-benchtime=2s -count=5）取**中位数**；等价性=真实语料对拍 + 随机模糊 + token 播种模糊 + 引擎 outcome 集采样（4 样本，冲突升级 64 样本）。
- 生产引擎锚点（本会话实测）：`BenchmarkModifyReqReal` 中位 **268,259 ns/op、78,448 B/op、394 allocs/op**（与任务书 "~394 次/78KB" 吻合）。

## 1. 探针 1：未特化正则的"最稀 token 反向索引"（Ghostery 技巧）——✅ 建议合入

### 1.1 前提验证（任务书要求的先决测量）

任务书前提"432 条正则 / ~404 条回落"经逐字对齐 ParseRule 语义后修正（ naïve 分类多算了 144 条 phantom：55,167 行 hosts 格式被误判 + 修饰符校验失败的规则从未入库）。**真实 store 口径**（直接枚举引擎两店）：

| 项 | 数值 |
|---|---|
| 正则规则总数 | **288**（primary 250 = 9 fast + 241 fallback；exception 38 = 4 fast + 34 fallback） |
| 回落（无 fastShape）正则 | **275** |
| 有"可证必需字面量段"(≥2B) | **262 / 275 = 95.3%**（caseA 单必需 token 252 + caseB 强制交替 one-of 集 10） |
| 必需段长度直方图 [0,1,2,3,4-7,8-15,16-31,32+] | [5, 14, 4, 18, 67, 102, 57, 8]（85% 有 ≥4 字节段） |
| 无 token 恒评估 | 13 条，全部廉价（≤1.3µs，纯类簇如 `[0-9]{5}`、`[a-z0-9]{8,}.[a-z]{3,}`、大小写混合 `[Ww]eb[Tt]racking`——后者被解析器折叠为 FoldCase，字节 token 不可证） |
| 隔离成本模型（5000 URL，摊销计时） | 回落正则总成本 104.5µs/req，其中 **80.8%（84.4µs）可跳过** |
| 候选数/URL | 平均 **29.1 / 275（-89.4%）**，最差 67 |

token 选择：每规则取"语料文档频率（含子串包含）最低"的必需段（并列取更长；`^`锚定前缀锁定段如 `http://` 选择性≈100% 无剪枝价值，仅当唯一时兜底，并被 caseB 交替集让位）。

### 1.2 设计与零漏报证明（概述）

- **必需段提取**（AST，sound-conservative）：只穿越"必然参与每次匹配"的构造（Concat、Capture、min≥1 的纯字面量 Repeat/Plus、单 ASCII rune 字符类如 `[.]`）；FoldCase 任一节点存在即放弃；非 ASCII 字面量、交替、可选重复全部截断（宁缺勿错）。
- **one-of 交替 token**（caseB）：顶层（或强制 concat 子节点内）交替组若每个分支都能提取必需段，则"至少出现一个分支 token"是必要条件——Ghostery reverse-index 同款。已处理 Go regexp/syntax 的**分支公共前缀因式分解**（`kimcartoon|kiss-anime` → `ki`+交替 → 重组为 `kimcartoon`/`kiss-anime`），重组 token 仅由强制字面量相邻段构成（可选间隙处中断，保连续性正确）。
- **查找**：URL 每位置取 token 前 min(len,4) 字节窗口查桶（map[uint32][]int32，栈上 4096 位候选位图），命中后 `strings.Contains` 全 token 复核再跑正则。k-gram 命中是必要非充分条件，复核+原 match 决策保证结果逐位一致。
- **顺序保持**：GetIdx 按原 regexp 切片顺序遍历、只做"可证跳过"，追加顺序与 Get 完全一致。
- **通用性（规则9）**：全部判据来自正则 AST 的一般格式属性与全局 df 统计；无任何对特定订阅/站点/字符串的分支。容量上限（4096 规则位图、64 token/交替组、128B token）为一般限制，超限走未索引线性扫描的 sound 兜底。
- 构建：惰性（首次 GetIdx，atomic CAS，Insert/Get 互斥契约同 Get），一次性 ≤3.7MB 分配（含瞬态），稳态每请求分配增量 **0**。

### 1.3 A/B 实测（5 轮中位数）

| 层级 | A（生产路径） | B（+token 索引） | Δ |
|---|---|---|---|
| store 级 Get（primary，5000 URL 轮转） | 141,093 ns/op | 52,311 ns/op | **-62.9%** |
| 引擎级 ModifyReq（3000 req 轮转） | 250,395 ns/op | 152,525 ns/op | **-39.1%**（远超 ≥5% 门槛） |

### 1.4 等价性证据（错配=0）

1. 真实语料：5000 URL × 两店 × GetIdx vs Get **多重集全等**（`TestTokenIdxEquivalenceReal`）。
2. 顺序确定性：纯正则 store（12 模式含全部难点形态）+ 200,000 模糊 URL（其中 10 万条 token 播种以打击 Contains 复核路径，含 `\n` URL）**逐元素全等**（`TestTokenIdxOrderEquivalence`）。
3. 引擎级：3000 URL × ModifyReq outcome 集（block/redirect/applied 集合）0 差异（`TestEngineFlagsEquivalence`，4 样本冲突时 64 样本升级）。
4. 附带发现（上游潜在缺陷，未修，规则5定性为"生产路径不可达"）：`internal/ruletree/ruletree.go:143-145` 对无 `://` 且长度 <3 的 URL 会 `url[hostStart:]` 越界 panic；生产调用面 renderURLWithoutPort 恒有 scheme，不触发。建议主线程知情后自行决定是否加防越界。

### 1.5 结论与落点

**建议合入**。引擎级 -39.1% ≥ 5% 门槛；等价 0 差异；代码增量 = `tokenindex.go` 529 行（非注释 377 行，全为生产相关：提取器+索引+构建）+ `rulestore.go` 集成约 15 行（idx 字段 + Get 内分支）；内存增量可忽略（一次性 ≤3.7MB/店 vs 引擎 live ~295MB，稳态 0）。
落点：新建 `internal/networkrules/tokenindex.go`；`internal/networkrules/rulestore.go` 的 `ruleStore` 加 `idx atomic.Pointer[reIdx]` 字段、`Get` 的 regexp 循环替换为 GetIdx 逻辑（token 构建惰性 + Compact 里主动构建均可）；`regexpRule.match` 不变。

## 2. 探针 2：剩余分配热点削减（GC ~15%）——✅ 建议合入（与探针 1 叠加）

### 2.1 热点定位（-memprofile，alloc_objects/-alloc_space，扣除装载期）

每请求（394 allocs / 78.4KB）构成：
| 热点 | 每请求 | 归属 |
|---|---|---|
| ruleStore.Get 结果切片 make+append 扩容链 | ~35.5KB | internal/networkrules/rulestore.go Get |
| filter() 拷贝（primary+exception 两次） | ~22KB | internal/networkrules/networkrules.go filter |
| traversePrefix 子遍历各自分配累加器 | ~10KB / ~143 obj | internal/ruletree/node.go |
| domainModifierEntry.MatchDomain（tld 路径/eTLD 缓存内部） | ~3.9KB / 67 obj | rulemodifiers/domain.go（internal，见"未试"） |
| RemoveParamModifier.ModifyQuery | ~3KB / 124 obj | rulemodifiers/removeparam.go（internal，见"未试"） |

### 2.2 子探针与 A/B（5 轮中位数，同二进制）

| 子探针 | 改动 | 实测 |
|---|---|---|
| 2a 共享累加器（traverser.visit 重构，子遍历不再各自建 traverser） | ruletreelp 副本 node.go 9 处调用点 | store Get：108→**47 allocs**、49.5→43.5KB、145.6→141.1µs；引擎 394→**304 allocs**（allocs 为确定性指标，跨二进制亦可信），CPU 268→250µs（跨二进制，含布局噪声） |
| 2b tree.GetLP（整个 Get 一个池化累加器 + 池化去重 map）+ 结果切片 sync.Pool（putRes 释放纪律，>8192 元素不入池）+ filterInPlace 原地压实 | ruletreelp GetLP + nrprobe rulestore/networkrules | 引擎 250.4→238.9µs（-4.6%）、**72,961→8,174 B/op（-88.8%）**、304→214 allocs；store GetLP：130,877 ns/op、**209 B/op、2 allocs**（-99.5% 字节） |
| 2c P1+P2 组合 | 全开 | 引擎 **140,190 ns/op（-44.0% vs 同二进制对照；相对生产锚点 -47.8%，跨二进制口径）、8,786 B/op（-88.8%）、229 allocs（相对生产 394 = -41.9%）** |

收益均 ≥2% 门槛，无丢弃项。

### 2.3 等价与安全证据

- 全部 5 个变体组合（GetIdx/GetLP/GetIdxLP/×noFast）× 真实语料 5000 URL × 两店多重集全等；200k 模糊（池化变体每 10 条抽检）0 差异；引擎级 4 组合 outcome 集 0 差异。
- 池释放纪律：ModifyReq/ModifyRes 中 `defer putRes`（defer 实参取原切片保 cap）；appliedRules/redirect 均为值拷贝，无池化背板逃逸；`maxPooledResEntries=8192` 防大切片囤积。
- 并发：8 goroutine × 2000 次混合 Get/GetIdx/GetLP/GetIdxLP 冒烟通过。**诚实声明：本机无 gcc/CGO，-race 不可用**；并发安全由构造论证（每 Get 独立取池、sync.Pool 线程安全、Get 侧零共享可变状态）+ 未插桩冒烟支撑，建议主线程合入后在有 CGO 的环境补跑 -race。

### 2.4 结论与落点

**建议合入**。落点：`internal/ruletree/node.go`（visit 重构，9 处）+ `internal/ruletree/ruletree.go`（GetLP 与两个池，~60 行）+ `internal/networkrules/rulestore.go`（res 池 + GetLP 分支，~50 行）+ `internal/networkrules/networkrules.go`（filterInPlace + defer putRes，~25 行）。合计 ~230 行。GC 压力随分配字节 -88.8% 同比下降（原 GC ~15% → 预计 ~2-4%，已含在上表 CPU 数字内）。

## 3. 探针 3：fastshape.go（1089 行）审视——❌ 不裁剪，保留

- **族普查**（真实 store，13 条 fast 规则 = 9 primary + 4 exception；真实覆盖率 13/288=4.5%，任务书"6.5%"含 phantom 口径）：scheme 前缀锚定 7、交替单位 5、needPath 1、atEnd 3、多单位链 4、endEmpty 0。**每个主要族都有 ≥1 条真实规则在用**，裁剪任一族=直接损失真实覆盖。
- **matchFast 零分配确认**：`testing.AllocsPerRun` × 13 规则 × 200 URL = **0 allocs/run** ✅。
- **引擎 A/B（决定性实验）**：token 索引开启后关闭 fastshape：152,525 → 156,244 ns/op（**+2.4%**）；P1+P2 组合上再关：140,063 → 144,580（**+3.2%**）。关闭是稳定负收益——matchFast 比这些规则的"索引复核+正则执行"仍快 ~3µs/req。
- 结论：**保留 1089 行**。它与 token 索引互补（fast 规则走 matchFast，275 条 fallback 走索引）；等价性由既有黄金/等价门禁守护；若未来追求代码量，唯一安全候选是把 endEmpty 等零覆盖分支折叠进 fallback——收益 ~0，不值得动。

## 4. 探针 4：64B 节点复核——一句话结案

`node[T]` = leaf 24B + prefixBase 8B + prefixLen 8B + edges 24B = **64B**（实测 `unsafe.Sizeof`）；要进 48B 尺寸类需再省 16B，但三个成员在内部节点同时活跃、无可合并（edges 头与 prefix 指针不能重叠），leaf/inner 分型会在热路径引入类型分支（B 轮 leaf 内联已证负收益）。litEdge=16B（backing 另分配）。**结案：无可省。**

## 5. 总建议与预期

| 项 | 决定 | 关键数字 |
|---|---|---|
| P1 token 反向索引 | **合入** | 引擎 -39.1%，store -62.9%，稳态分配增量 0，代码 ~545 行 |
| P2 低分配路径（共享累加器+池+原地压实） | **合入** | 叠加后引擎 **-44.0%**（同二进制）/ -47.8%（对生产锚点），分配字节 **-88.8%**，allocs -41.9%（对生产），~230 行 |
| fastshape.go | **保留** | 关闭 +2.4~3.2% CPU；零分配；各主要族有真实覆盖 |
| 节点结构 | **不动** | 64B 已是边界 |

P1+P2 合入后预估：ModifyReq ~140µs/req（相对本会话生产锚点 268µs），分配 8.8KB/req——GC 与 map/tree 两大剩余热点同时大幅收敛。全部数字为 5 轮中位数，复跑命令见各测试文件注释。

## 6. 诚实声明：什么没试、为什么

1. **-race 插桩**：本机无 gcc/CGO，无法运行 race detector；并发安全以构造论证+未插桩 8 线程冒烟代替（探针 2.3）。建议合入侧补跑。
2. **Header.Get 直读 canonical 键**（省 textproto 校验扫描，profile 上限 ~4.7%）：未实现。理由：P1/P2 合入后其相对占比进一步缩水，预期 <2% 门槛；改动虽小（ModifyReq 两处直读 `h["Sec-Fetch-User"]`）但收益不确定，留给下一轮。
3. **MatchDomain / ModifyQuery 内部分配**（~7KB/req 上限，剩余分配的大头）：internal 的 rulemodifiers/rule/exceptionrule 三包需要整体复制才能探针 A/B，投入产出比低；且 RemoveParam 的 clear(query)+重编码语义牵动 redirectURL 黄金对拍。落点已标注（domain.go tld 路径、removeparam.go ModifyQuery），留独立立项。
4. **大小写折叠 token**（`[Ww]eb[Tt]racking` 类，13 条恒评估中的少数）：可用小写镜像 URL 做索引，但涉及字节/Unicode case 语义（U+212A 类折叠），正确性论证成本高于 13 条廉价规则的收益，未试。
5. **gctrace 端到端 GC 次数对比、真实代理流量 E2E**：未测（分配字节 -88.8% 已由 benchmem 证明；E2E 与既有门禁同样不可行，标注未验证维度）。
6. 探针 1 的 store 级 A/B 在两店（primary/exception）各自独立成立；exception 店规则数少（38），其索引收益占比小，未单独出报告数字。

## 7. 复跑索引

| 装置 | 命令（在 vt_c 内） |
|---|---|
| 前提普查 | `go test ./tmp_enginebench/nrprobe/ -run TestCensusTokenPremise -v` |
| 提取器诊断 | `go test ./tmp_enginebench/nrprobe/ -run TestDebugExtractor -v` |
| 等价门（全变体） | `go test ./tmp_enginebench/nrprobe/ -run 'TestTokenIdx|TestEngineFlagsEquivalence' -v` |
| A/B 基准矩阵 | `go test ./tmp_enginebench/nrprobe/ -run '^$' -bench 'BenchmarkProbeModifyReqReal|BenchmarkProbeStoreGet' -benchtime=2s -count=5` |
| 生产锚点 | `go test ./tmp_enginebench/ -run '^$' -bench 'BenchmarkModifyReqReal$' -benchtime=2s -count=3` |
| 并发冒烟 | `go test ./tmp_enginebench/nrprobe/ -run TestGetLPParallelSafe -v` |
| 探针 3 | `go test ./tmp_enginebench/nrprobe/ -run 'TestFastShape' -v` |
| 探针 4 | `go test ./tmp_enginebench/ruletreelp/ -run TestNodeSizeClass -v` |

探针代码：`vt_c\tmp_enginebench\nrprobe\`（tokenindex.go=提取器+索引；rulestore.go/networkrules.go=引擎副本+四路开关；probe1_census_test.go / probe1_equiv_test.go / probe2_parallel_test.go / probe3_fastshape_test.go / probe1_debug_test.go）、`vt_c\tmp_enginebench\ruletreelp\`（internal/ruletree 逐字副本+visit/GetLP 补丁）。
