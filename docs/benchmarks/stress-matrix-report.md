# 引擎复杂环境与压力测试矩阵报告（stress_matrix_report）

- 日期：2026-10-05
- 工作树：`<worktree>`（upstream-chain；含未 commit 的引擎优化代码）
- 测试代码：`vt_a\tmp_enginebench\stress_test.go` / `stress_adversarial_test.go` / `stress_env_test.go`（本次新增，仅此三文件）
- 约束遵守：未修改 `internal/` 与 `tmp_enginebench/baselinenr/` 任何代码（`git status` 中 internal/ 改动均为任务开始前已存在的优化代码；本次会话只在 untracked 的 tmp_enginebench/ 下新增测试文件）
- 对拍口径：**基线 = baselinenr（改前引擎冻结副本）**，**生产 = internal/networkrules（优化后）**

---

## 0. 一句话结论

**全部 6 个新测试 + 1 个既有对抗门禁 PASS；两引擎在所有矩阵维度上行为零分歧（含 panic 行为逐项相同）；未发现本次优化引入的新 bug。** 唯一"异常"是短 URL 越界 panic——两引擎代码同源、行为完全一致，属**上游既有隐患**（详见 §6），非本次优化引入。

---

## 1. 运行命令与总结果（真实输出）

```
$ go test -count=1 -run 'Stress|Env|Adversarial' ./tmp_enginebench/ -v -timeout 30m
```

```
=== RUN   TestGoldenAdversarialRules        （既有 59 条对抗门禁，顺带复跑）
--- PASS: TestGoldenAdversarialRules (1.07s)
=== RUN   TestAdversarialRulesMatrix
--- PASS: TestAdversarialRulesMatrix (0.01s)
=== RUN   TestAdversarialURLMatrix
--- PASS: TestAdversarialURLMatrix (0.00s)
=== RUN   TestEnvMatrixGCPressure
--- PASS: TestEnvMatrixGCPressure (5.91s)
=== RUN   TestStressConcurrentMixedTraffic
--- PASS: TestStressConcurrentMixedTraffic (45.08s)
=== RUN   TestStressCacheRebuildUnderConcurrency
--- PASS: TestStressCacheRebuildUnderConcurrency (0.35s)
=== RUN   TestStressTreeInsertGetLifecycle
--- PASS: TestStressTreeInsertGetLifecycle (0.00s)
PASS
ok    github.com/irbis-sh/zen-desktop/tmp_enginebench    53.048s
```

语料：真实订阅缓存 `LOCALAPPDATA\Zen\filters\*.cache.txt`，去重后 **611,652 行**（stress_test.go:168 实测）。

---

## 2. 并发压力（重点）——32 goroutine × 10 万混合流量

**TestStressConcurrentMixedTraffic**（stress_test.go）：2000 URL × 8 头组合 × referer 轮转 = 16,000 互异 (url, combo, referer) 三元组全表做**串行参考**；32 goroutine 消费 100,000 op（7919 质数步进打散访问序），每 op 新建独立请求、recover 包裹，**逐 op 与串行参考比对**（shouldBlock / redirectURL / appliedRules 集合 / ModifyRes 结果 / panic 状态，比对口径与 golden_test 相同：阻塞态只比三元组长度，因阻塞单条本就依赖 map 遍历序）。两引擎各跑 ×2 轮：

```
baseline   第1轮: 32 goroutine × 100000 op 全部完成，0 panic，0 与串行不一致（耗时 7.17s）
production 第1轮: 32 goroutine × 100000 op 全部完成，0 panic，0 与串行不一致（耗时 5.52s）
baseline   第2轮: 32 goroutine × 100000 op 全部完成，0 panic，0 与串行不一致（耗时 7.03s）
production 第2轮: 32 goroutine × 100000 op 全部完成，0 panic，0 与串行不一致（耗时 5.62s）
```

另做两引擎**串行参考交叉复核**（16,000 三元组）：0 处不一致。

**结论维度：新引擎 ≥ 旧引擎**（两者各自并发结果与自身串行逐 op 一致；生产引擎还快约 23%）。⚠️ 证据等级注明：`-race` 不可用（见 §8），这是"高并发重复 + 结果集一致"的**替代证据，弱于 race detector**。

---

## 3. 缓存重建定向压力（refererHostCache / eTLD1Cache）

**TestStressCacheRebuildUnderConcurrency**（stress_test.go）：针对 `internal/networkrules/rulemodifiers/domain.go:67`（refererHostCacheLimit=4096）与 `domain.go:164`（eTLD1CacheLimit=4096）的"满即整体重建"设计（重建分支 domain.go:93-95 / 190-193）。用 **6000 个不同 referer**（> 4096，重建必然发生）灌入 4 条 $domain 规则（常规条目 / tld 条目 → eTLD1 缓存 / 正则条目 / exception 的 Cancels 路径），32 goroutine × 100,000 op，37 互质步进轮转保证全部键被多 goroutine 交错读写：

```
串行参考：blocked=3000 clean=3000（$domain 匹配与豁免两路均覆盖）
生产引擎缓存重建并发：10 万 op 全部完成，0 panic，0 与串行不一致（6000 键 > 4096 上限 → 整体重建必然发生）
基线引擎（无缓存）并发：10 万 op 全部完成，0 panic，0 与串行不一致
```

- 覆盖自检：命中类与不命中类各 3000（若全 0 说明 $domain 路径没被真实执行，测试会自行 Fatal）。
- 生产引擎（带缓存）与基线引擎（无缓存）串行结果逐 referer 交叉一致 → **缓存是纯语义透明的 memo**。
- 测试过程开发记录（诚实留痕）：首轮失败根因是**测试用例自身 bug**（规则正则 `^h[0-9]+\.` 恰好匹配了 clean 类 referer 主机前缀），修正正则后通过——与引擎无关。

**结论维度：新引擎 ≥ 旧引擎**（重建风暴下无死锁、无 panic、结果与串行及无缓存基线全部一致）。

---

## 4. Insert / Get / Compact 生命周期（"不得并发"契约的顺序验证）

**TestStressTreeInsertGetLifecycle**（stress_test.go）：契约出处 `internal/ruletree/ruletree.go:15`（"Insert and Compact must not run concurrently with Get"）。顺序交替：批量 Insert batch1（含 `****`/`^^^^`/`|||`/NUL/emoji/unicode/64KB pattern/同 pattern 双 value/空 pattern no-op）→ 批量 Get → 再 Insert batch2 → 再 Get → **Compact** → 再 Get。生产树（internal/ruletree，64B 节点优化版）与基线树逐阶段对拍：

```
生命周期矩阵：跨树差异 batch1=0 batch2=0 compact=0；插入回退=0；Compact 回退 base=0 prod=0；命中探针=9/9
```

- Compact 前后结果集逐 URL 全等（两树都是）→ Compact 只缩容量不改语义。
- 插入单调性：batch1 的全部命中在 batch2 插入后无一丢失。
- 命中自检 9/9（防断言空转）。

**结论维度：新引擎 ≥ 旧引擎**。

---

## 5. 规则侧恶劣输入矩阵（ParseRule，40 项，双引擎逐项）

**TestAdversarialRulesMatrix**（stress_adversarial_test.go）。每项两引擎各 ParseRule 一次：接受性必须一致；双方接受时用 4 探针 URL × 2 头组合过 ModifyReq/ModifyRes（recover 包裹），比对行为与 panic：

```
=== 规则侧恶劣输入矩阵：40 项 ===
汇总：双方接受=28 双方拒收=12 差异=0 panic=0
```

| 类别 | 项 | 判定 |
|---|---|---|
| 全 `*` 串 / 50 段交替通配 / 仅分隔符 `^^^^` / 仅锚 `\|\|\|` / 单字符锚 `\|` | 5 | 双方接受，探针一致 |
| `$` 修饰符空值（尾随$ / 空 domain / 空 method）、重复（script,script / 重复domain）、未知（含带值）、`$$`、`$important=all` | 9 | 空值类+未知类+`$important=all` 双方拒收；重复类双方接受 |
| `$domain=` 1000 项 | 1 | 双方接受，探针一致 |
| `$domain=` unicode+punycode（纯） / unicode+punycode 混反选 | 2 | 纯：双方接受；混反选：双方拒收（cannot mix inverted...） |
| hosts 行（0.0.0.0 / 127.0.0.1 / 多主机 / 注释 / IPv6 / 2000 主机超长行） | 6 | 双方接受，探针一致 |
| 1MB 单行 / NUL 字节 / 非法 UTF-8×2 / emoji×2 / 仅空白×2 | 9 | 双方接受，探针一致 |
| 正则 `(?i)` / `\b` / `{1000}` 边界 | 3 | 双方接受，探针一致 |
| 正则 环视 lookahead / lookbehind / `{10000}` / 空体 `//` | 4 | 双方拒收（RE2 编译错误 / empty regexp rule，错误信息逐字一致） |

**结论维度：新引擎 = 旧引擎**（40/40 接受性一致、错误信息一致、0 panic、0 行为差异）。12 项双方拒收中，错误信息前缀逐字相同（parse modifiers:… / insert rule:…），说明解析路径语义未被优化改动。

---

## 6. URL 侧恶劣输入矩阵（32 项，🔴 重点：短 URL panic 双引擎对比）

**TestAdversarialURLMatrix**（stress_adversarial_test.go），两层：树级（两棵同内容树 `Tree.Get` **直收原始串**）+ 引擎级（ModifyReq/ModifyRes 全链路）。

```
URL 矩阵汇总：树级 panic 分歧=0 结果集分歧=0 双方 panic=7/32；引擎级 panic 分歧=0 双方 panic=7 url.Parse 拒收=7
```

### 6.1 精确 panic 边界（重点发现，如实记录）

隐患代码位置（两引擎同源）：
- 生产：`internal/ruletree/ruletree.go:136` —— `hostEnd = strings.IndexAny(url[hostStart:], "/?")`，其中 `hostStart = strings.Index(url,"://")+3`（无 `://` 时恒为 2）
- 基线：`tmp_enginebench/baselinenr/ruletree/ruletree.go:136` —— 逐字相同

**树级实测**（原始串直灌 Get）：
- `len(url) == 0` → 双方 panic `slice bounds out of range [2:0]`
- `len(url) == 1`（`/`、`x`、`a`、`?`、`#`、`%`）→ 双方 panic `[2:1]`
- `len(url) == 2` 起（`ab`、`//`、`://`…）→ 双方均不 panic
- 即任务书所称"1-2 字符隐患"的**精确边界是：len<2 且不含 `://`**（2 字符实际安全）

**引擎级实测**（ModifyReq 链路，`renderURLWithoutPort` 归一化后进 Get，networkrules.go:144-153）：
- panic 集 = `{""、"/"、"x"、"a"、"//"、"?"、"#"}` 共 7 项，双方逐项相同
- 关键细节：`"//"`（2 字符）、`"?"`、`"#"（1 字符）在树级**不** panic，但经 `renderURLWithoutPort`（剥端口/fragment、空 host 归零）后**归约为空串** → 引擎级 panic `[2:0]`。即引擎级 panic 面比树级更宽，且**这一行为两引擎完全一致**

### 6.2 定性（规则5：逻辑需要 vs 真实影响）

- **这是上游既有隐患，不是本次优化引入的**：两份代码该函数逐字同源，panic 边界逐项相同（分歧=0），优化前即如此。
- **真实影响评估**：触发需要 `ModifyReq` 收到渲染后长度 <2 且无 scheme 的 URL——正常代理流量的请求行不会是空串/host-only；但 `url.Parse("//")`、`url.Parse("?")` 这类可解析的病态输入一旦到达（如恶意/畸形上游响应构造的请求）即触发。**属健壮性缺口，值得单独上报上游修复（如在 Get 入口对 len<hostStart 提前返回空集）；本次按任务约束只记录不修。**
- 结果集一致性：所有不 panic 的 probe（含 IPv6 字面量、IDN/punycode、百分号编码、无 scheme、多 scheme、含 `\n` 裸串、超深路径 4000 段、重复分隔符、64KB、NUL、非法 UTF-8、大写 scheme、ftp scheme）两树结果集逐项相同。

**结论维度：新引擎 = 旧引擎**（含 panic 行为在内 32/32 逐项一致；发现的是共同的上游既有隐患，已如实记录并定性）。

---

## 7. 环境矩阵（7 组配置）

**TestEnvMatrixGCPressure**（stress_env_test.go）。口径：每 4 行取 1 的真实语料抽样（152,913 行/引擎，活堆 ≈110MB）+ 2000 URL 抽样，每组配置跑一遍双引擎全链路等价 sweep，比对 (a) 组内跨引擎一致性 (b) 生产引擎行为签名（sha256 前 8 字节）跨组恒定性 (c) GC 统计佐证压力真实生效。GOMAXPROCS/GOGC/GOMEMLIMIT 经 runtime/debug 设定（与环境变量初始化的是同一组运行时开关），另用真·环境变量复跑对抗矩阵三组作补充证据。

| 配置 | 跨引擎不一致 | 行为签名 | 耗时 | GC 次数 | 暂停 | 累计分配 | 判定 |
|---|---|---|---|---|---|---|---|
| GOMAXPROCS=默认(18) | 0 | 31e1722f ✅ | 687ms | 2 | 0.0ms | 192MB | ✅ |
| GOMAXPROCS=1 | 0 | 31e1722f ✅ | 810ms | 1 | 0.0ms | 191MB | ✅ |
| GOMAXPROCS=2 | 0 | 31e1722f ✅ | 729ms | 2 | 0.0ms | 191MB | ✅ |
| GOGC=off | 0 | 31e1722f ✅ | 683ms | 0 | 0.0ms | 191MB | ✅ |
| GOGC=10 | 0 | 31e1722f ✅ | 790ms | **22** | 10.5ms | 195MB | ✅ |
| GOGC=400 | 0 | 31e1722f ✅ | 683ms | 0 | 0.0ms | 191MB | ✅ |
| GOMEMLIMIT=300MiB | 0 | 31e1722f ✅ | 725ms | 1 | 1.0ms | 191MB | ✅ |

真·环境变量注入复跑（对抗矩阵全量，各 exit=0）：
```
GOMAXPROCS=1     go test -run 'Adversarial' → exit=0
GOGC=10          go test -run 'Adversarial' → exit=0
GOMEMLIMIT=300MiB go test -run 'Adversarial' → exit=0
```

**结论维度：新引擎 ≥ 旧引擎**（7 组配置下行为签名逐位恒定 31e1722f，跨引擎全程一致）。

压力真实性诚实标注：
- GOGC=10 档 22 次 GC + 10.5ms 暂停 → **GC 压力真实生效**；
- GOMEMLIMIT=300MiB 档仅 1 次 GC（抽样活堆 ~110MB 未逼近限额）→ **该档压力偏弱**，结论强度低于 GOGC=10 档；全量语料（双引擎活堆 ≈460MB）配 300MiB 限额属"限额低于活堆"的 GC 死亡螺旋病态配置，不构成有效等价性信号，故未采用。

---

## 8. race 检测可用性（证据链）

本机 **race detector 不可用**，三步取证：

```
1) gcc --version          → bash: gcc: command not found
   go env CGO_ENABLED     → 0
2) go test -race（最小包） → go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
3) CGO_ENABLED=1 go test -race（最小包）
                          → cgo: C compiler "gcc" not found: exec: "gcc": executable file not found in %PATH%
   which gcc/cc/clang/zig/tcc → 全部不存在
```

**替代证据（弱于 -race，如实声明）**：GOMAXPROCS=默认(18 逻辑核，`NUMBER_OF_PROCESSORS=18`) × 32 goroutine × 10 万 op × 2 轮 × 2 引擎，逐 op 与串行参考比对全一致（§2）；缓存重建风暴 10 万 op 一致（§3）。数据竞争若存在，多轮 18 核并发下结果偏离串行的概率极高，但仍不能完全排除低概率竞态——**此为本次矩阵的最大证据缺口**。

---

## 9. 两引擎行为差异清单

**空。** 全部矩阵（并发 40 万 op、规则 40 项、URL 32 项×2 层、环境 7 组）中两引擎可观测行为差异 = 0；错误信息逐字一致；panic 集合逐项相同。唯一异常发现（短 URL panic）为**双方共同的上游既有行为**，非差异、非新引入（§6）。

---

## 10. 诚实声明（什么没测到）

1. **race detector 缺位**（§8）——数据竞争仅有替代证据，无 -race 铁证。
2. GOMEMLIMIT=300MiB 档 GC 压力偏弱（1 次 GC）；"限额低于活堆"的病态组合未跑（死亡螺旋无信号价值）。
3. url.Parse 拒收的 7 条串（`://`、`://x`、`x://`、含 `\n`×2、非法 UTF-8、`%`）只达树级（Tree.Get 直收原始串语义），引擎级不可达——已分层记录。
4. 并发混合流量覆盖 16,000 互异三元组 × 40 万次执行，非全语料×全 URL 笛卡尔积（该维度由既有 golden 对拍 36,623 对全一致补充）。
5. ModifyRes 只覆盖 200 text/html 形态（与 golden 同口径）；jsonprune/removeheader 等 res 侧 modifier 的深度行为不在本矩阵（由 internal 单测覆盖）。
6. 环境矩阵 7 组中 4 组为 runtime/debug 开关等价设定 + 3 组真 env 注入复跑；未做 7 组全真 env 矩阵。
7. 未覆盖 HTTP/2、WebSocket 等协议特有流量形态（引擎接口只消费 *http.Request/*http.Response）。
8. 测试过程曾出现两次**测试用例自身 bug**（缓存测试正则误伤 clean 类；$domain unicode 用例混入 `~` 导致走混合反选拒收分支），均已修正并留痕——引擎本身从未出现需"让测试通过"的改动。

---

## 11. 总结论（逐维度）

| 维度 | 判定 | 证据 |
|---|---|---|
| 并发一致性（10 万×2轮×2引擎） | **新 ≥ 旧**（生产还快 ~23%） | §2（替代证据，race 缺位已声明） |
| 缓存重建并发安全（4096 上限风暴） | **新 ≥ 旧** | §3，且缓存语义与无缓存基线逐项一致 |
| Insert/Get/Compact 生命周期 | **新 ≥ 旧** | §4，Compact 前后 0 回退 |
| 恶劣规则输入（40 项） | **新 = 旧**（接受性/错误信息/行为全同） | §5，0 差异 0 panic |
| 恶劣 URL 输入（32 项×2 层） | **新 = 旧**（含 panic 行为逐项相同） | §6 |
| 环境矩阵（7 组） | **新 ≥ 旧**（签名跨配置恒定） | §7 |
| 新引入 bug | **未发现** | 全部矩阵 0 分歧 |

**总判定：在本次矩阵覆盖范围内，新引擎（优化后）在恶劣环境与压力下的可观测行为与旧引擎完全一致且性能不劣；短 URL 越界 panic 为上游既有隐患（两引擎同源一致），建议单独上报修复，不构成对本次优化的否决。**

---

*证据等级：本报告所有数字均来自 `go test` 真实输出（/tmp/stress_final.log 及三组 env 复跑日志）；测试代码可复跑：`go test -count=1 -run 'Stress|Env|Adversarial' ./tmp_enginebench/ -v`。*
