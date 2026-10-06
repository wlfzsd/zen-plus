# tmp_enginebench — 引擎验证装置 / Engine Verification Harness

本目录是 zen-plus 过滤引擎全部性能与正确性验证的基准装置（对拍、黄金回放、
压力、真实订阅语料装载器、全维度基准）。`docs/benchmarks/` 里的每份报告
都由这里的测试产出，可复跑。

This directory is the verification harness behind every benchmark and
correctness claim in `docs/benchmarks/`. Everything is reproducible.

## 组成 / Layout

| 路径 | 作用 / Purpose |
|---|---|
| `bench_test.go` | 真实订阅语料装载器（`loadRealLines`）、ModifyReq 端到端基准、组件微基准 |
| `golden_test.go` | 黄金对拍：冻结基线引擎 vs 生产引擎，3.6 万+ 请求三元组逐一对拍 |
| `equiv_test.go` | 1 万条真实 URL 的结果集等价对拍 |
| `stress_test.go` | 32 线程并发压力、恶劣规则/URL 矩阵、环境变量矩阵（GOMAXPROCS/GOGC/GOMEMLIMIT） |
| `adtest_test.go` + `adtest_urls.txt` + `adtest_urls_meta.txt` | 广告拦截测试页（d3ward 等 6 种机制）探测 URL 数据集与有效性判定 |
| `fullmatrix_bench_test.go` | 全维度对比基准（装载耗时/树吞吐/ModifyRes/逐行兼容） |
| `idx_invalidation_test.go` | token 索引在"建索引后再插规则"场景的失效专项测试 |
| `baselinenr/` | 优化前引擎的冻结副本（对拍基线，Package 名与生产引擎相同） |
| `nrprobe/`, `ruletreelp/` | P1 token 索引与 P2 低分配路径的探针源码（已合入生产，留档复现） |
| `ruletreeopt/` | 早期树优化原型（研究留档，未全部采纳） |
| `gen_adtest_urls.py` | 广告探测 URL 数据集生成器 |

## 复跑 / Reproducing

```bash
# 1) 纯语料无关部分（无需任何本地数据）：
go test ./tmp_enginebench/ -run 'TestGolden|TestEngineEquivalence|TestTokenIdx'

# 2) 真实订阅语料部分（默认读取 %LOCALAPPDATA%\Zen\filters\*.cache.txt，
#    其他机器设置环境变量指向含 .cache.txt 的目录）：
set ZEN_FILTER_DIR=D:\path\to\filters
go test ./tmp_enginebench/ -run 'TestNetworkRulesLiveMemoryReal' -v
go test ./tmp_enginebench/ -bench 'BenchmarkModifyReqReal' -benchtime=2s

# 3) 基准对照：BenchmarkModifyReqRealBaseline 跑冻结基线引擎，
#    BenchmarkModifyReqReal 跑当前引擎，同机交替取中位数。
```

## 数据文件说明 / Data files

- `easylist.txt`、`easyprivacy.txt`、`urls.txt` 直接引用上游已提交的
  `internal/ruletree/testdata/`，本目录不再重复存放。
- d3ward 等测试页的页面快照（`adblock_data.json`、`*.html`、`d3host_raw.txt`）
  版权归原方，**不入库**（见 `.gitignore`）；需要复跑 d3ward 数据生成时，
  用 `gen_adtest_urls.py` 依其官方页面自行抓取，或删去对应测试项。
  Page snapshots are copyrighted by their authors and excluded from the
  repository; regenerate locally with `gen_adtest_urls.py` if needed.
- 装置内 `nrprobe/`、`ruletreelp/`、`baselinenr/` 均为引擎的独立副本
  （探针对照与冻结基线用），与生产代码的同步点以 `docs/benchmarks/`
  各报告记录的提交为准。
