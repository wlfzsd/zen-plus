# zen-plus 五版本演化对比（官方 v0.27.1 = 基线 1.0×）

> 实测时点：2026-10-15。装置：abbench（同一份 harness 跨版本逐字节一致），5 个 git worktree
> 分别检出 official v0.27.1 (195d2ca) / v3.0.0 (41e49cb) / v3.1.0 (1c06a52) / v3.1.8 (9728010) / v3.2.0 (8278bc7)。
> 语料：urls.txt 33,303 + obfusgated/adtest TSV = 35,358 probes/遍；13 列表生产同构装载（同一 %LOCALAPPDATA%\Zen\filters 缓存）。
> 内存两轮零波动（5 版全绿）；CPU 为两轮均值；单线程计时。

## 总表（官方 = 1.0×）

| 版本（官方=基线） | 请求匹配/req | XHR 匹配/req | 响应改写/req | ResXHR/req | 每请求分配 | 引擎驻留（GC 后） | 装载峰值 | 实装规则/解析失败 | 装载耗时 |
|---|---|---|---|---|---|---|---|---|---|
| **官方 v0.27.1** | 558.8 µs | 618.8 µs | 305.0 µs | 298.7 µs | 146.0 KB / 1,625 次 | 150.6 MB（100%） | 288.8 MB | 556,143 / 11,885 | 993 ms |
| zen-plus v3.0.0 | 278.1 µs（2.0×） | 293.7 µs（2.1×） | 123.2 µs（2.5×） | 118.6 µs（2.5×） | 16.9 KB / 541 次 | 179.2 MB（119%） | 323.7 MB | 556,143 / 11,856 | 2,061 ms |
| zen-plus v3.1.0 | 152.4 µs（3.7×） | 174.1 µs（3.6×） | 116.7 µs（2.6×） | 122.1 µs（2.5×） | 2.2 KB / 14 次 | 191.6 MB（127%） | 398.1 MB | 565,634 / 2,365 | 2,458 ms |
| zen-plus v3.1.8 | 150.0 µs（3.7×） | 176.6 µs（3.5×） | 113.9 µs（2.7×） | 116.8 µs（2.6×） | 2.3 KB / 14 次 | 135.3 MB（90%） | 317.7 MB | 565,634 / 2,394 | 1,350 ms |
| **zen-plus v3.2.0（当前线）** | **102.0 µs（5.5×）** | **119.6 µs（5.2×）** | **77.7 µs（3.9×）** | **76.7 µs（3.9×）** | **2.2 KB / 15 次** | **130.9 MB（87%）** | **301.0 MB** | 565,634 / 2,394 | 1,354 ms |

## 每版本里程碑（引擎侧）

- **v3.0.0**（41e49cb）：P6 全状态记忆化、4096 截断、异常三桶分类、noConds 快路径之前的基线＝B1-B8 兼容批次之前。
- **v3.1.0**（1c06a52）：B1-B8 AdGuard 语法兼容（多救 9,491 条）、query-state pooling＋actionindex（每请求分配 16.9→2.2 KB）、覆盖去重、P6、actionindex。
- **v3.1.8**（9728010）：**1c 打包字节前缀**（tokenize 41.5→tokenArena 7.45 MB，live −29.4%）、反向索引分支集（altfix）、#818 !#if、modsInterner、freeze/liveExc。
- **v3.2.0**（8278bc7）：**WS1 域门控**（正则侧 −26.21%）＋**hostSplit**（24.3 万条 ||host^ 出树进 O(1) map，−4.8 MB）＋**T1 树候选域位图**（MatchDomain 8.5%→残余 5.06%）；位图已池化（9c180fe，−160 B/req）。

## 反直觉点的机制解释（四版本堆/分配画像实测）

1. **v3.1.0 驻留反升**（179.2→191.6 MB）：不是优化让内存变大——是 B1-B8 兼容批次救回 +9,491 条规则，每条带完整修饰符解析对象（ParseRule flat +5.5 MB、RawRule 文本 +4.5 MB、DomainModifier +4.1 MB）。
2. **v3.0.0 每请求 16.9 KB 的来源**：RemoveParamModifier.ModifyQuery（995.5 MB/遍，全进程分配的 23.7%）＋MatchDomain（460 MB）——当时空 pattern 动作规则堆在 generic 桶每请求全量解析查询串。1c8f7eb（查询状态池化）＋b8d75a0（actionindex）把它们杀掉。
3. **v3.2.0 分配 +1.9 次/req**：T1 filterTree 的每请求资格位图（treegate.go）——已池化回收（9c180fe），字节回落到 v3.1.0 水平，域门控的 −47 µs/req 收益保留。

## 交叉验证

- v3.1.8 本轮 150.0 µs / 135.3 MB vs 10-14 首测 134.8 µs / 135.5 MB——CPU 差 11%（语料微更新 1,023 条＋机器态），内存几乎逐位一致；倍率口径同量级复现（4.0×→3.7×）。
- 每规则驻留：官方 284 B/条 → v3.2.0 243 B/条（−14.4%）。
- 黄金 dump（池化前后同语料）：4770819efa97f487 逐位一致（与 10-14 基线 5d416705462fec69 的差异源自 10-15 订阅更新器刷新的列表缓存，非代码差异——v320 worktree 对照实验证实）。

## 复现

\`\`\`powershell
# 每版本一个 worktree，同一份 abbench harness：
git worktree add D:\zbench\<name> <tag>
# 环境变量：ZEN_AB_RUN=1、ZEN_AB_CORPUS=<仓库>/abbench/corpus、ZEN_AB_OUT=<输出目录>、ZEN_AB_TAG=<标签>
go test ./abbench/ -run TestAbMemory -timeout 20m -v   # 内存两轮
go test ./abbench/ -bench . -benchtime 1s -count 3 -timeout 30m   # CPU
\`\`\`
