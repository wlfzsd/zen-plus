# status=active | date=2026-11-20 | verified_at=2026-11-20 | depends=wails.json | supersedes= | scope=zen-plus 分支（upstream-chain）自 v3.2.1 起的版本变更记录

本文件只记录 zen-plus 分支自己的改动（上游 irbis-sh/zen-desktop 的变更见仓库自带 `CHANGELOG.md`）。
版本号来源：`wails.json` 的 `productVersion`；打标签在同一提交上做 `vX.Y.Z`。

## v3.2.3（2026-11-20，已装机）

**内容类型语义（学上游 b09b676，用本地结构重写）**
- 新增 `internal/networkrules/contenttype` 包：12 位类型位掩码、`ByName`、`FromRequest`、`FromResponse`；每请求只分类一次。
- `internal/networkrules/rule/rule.go`：内容类型修饰符从"或条件组"改为掩码累加，`$~a,~b`（全部否定）不再恒真；`$~all` 仍拒绝解析。
- `internal/networkrules/networkrules.go`：请求/响应两侧统一带类型判定；`ModifyRes` 跳过没有动作修饰符的规则（谓词下推）。
- `internal/networkrules/exceptionrule/exceptionrule.go`、`coveragededup.go`：带内容类型条件的规则不再被当作"裸白名单/可去重"。
- 有意差异：`FromResponse` 不做 Content-Type 回退，认不出类型归 Other（防原生 App 的 JSON 响应被"异步请求"规则命中），由 `internal/networkrules/response_domain_test.go` 的用例钉死。

**响应体按原编码回写（学上游 0cc69c4 的不变量）**
- `internal/httprewrite/rawbody.go` 重写为 `rawBody`：只解压、不改 `Content-Type`；UTF-16 例外转 UTF-8 并重贴标签；GBK/Shift_JIS 等传统编码解码给改写器后按原编码写回。
- 修复旧行为：改写后一律把字符集标成 UTF-8，导致 GBK 页面乱码、依赖原文的改写规则失效。

**测试**
- 新增 `internal/networkrules/contenttype/contenttype_test.go`、`internal/networkrules/rule/contenttype_test.go`；重写 `internal/httprewrite/rawbody_test.go`（保留 2026-10-14 三条空响应体回归）。
- `go test ./internal/... -count=1`：除 `internal/process/pid_test.go:20`（环境性 EvalSymlinks 失败，同族工作树复现）外全通过。

**产物**
- `Zen_prod_contenttype.exe` sha256 `B3FFF15B20915B24107BA747BCC59013CCF05D1971B81A7F080701A4D71A7E51`（28,724,224 字节）。

## v3.2.2（2026-11-20，已装机）

**上游零冲突提交移植（7 条，cherry-pick，无冲突）**
- `66f8927` 日语/越南语语言包；`b5812ad` 阻断页与错误页本地化；`58e0e37` 取消文件对话框不再算失败；`e848a3d` Taskfile 增加 flake.lock 任务；`367817f` 前端错误 toast 本地化；`808c2eb` 匈牙利语；`bc1a66d` 删除冗余 CLAUDE.md。

**新增 i18n 包**：`internal/i18n`（语言解析与文案表）。

**产物**
- `Zen_upstream_ux.exe` sha256 `d5d6b9d82be0643d8f1ef7de95706c0ce3fee645bc6b9e88ab084d3a8960562f`（28,711,936 字节）。

## v3.2.1（2026-11-20，已装机）

**代理：透明隧道粘滞失效修复（本分支核心修复）**
- `internal/proxy/proxy.go`：`transparentHosts` 从"进程内永久禁止解密"改为"1 分钟后自动重试 + 2/4/8/10 分钟封顶退避 + 成功即清除"。
- 日志增加主机指纹与重试间隔：`adding <host> to ignored hosts (fp=… watched=… leg=inbound|outbound retry_in=…)`；成功清空时写 `latch cleared for host fp=…`。
- 新增 `internal/proxy/latch_test.go` 五个用例（到期重试、退避封顶、清除后重新解密、陈旧条目回收、指纹稳定）。
- 背景：上游把"TLS 握手失败"当作"该主机不能解密"的信号并无期限放行（fail-open 属有意设计），本地此前也是永久性；表现为"Zen 开着但某站点广告突然不再被拦，停掉拦截再开就好"。

**产物**
- `Zen_latchfix.exe` sha256 `E1FFED06073654CE1AB8A7A940EEDFD5384FE4E9C1DAB96207791E84A35D6941`（28,669,952 字节）。

## 发布流程（每次发版照做）

1. 改 `wails.json` 的 `productVersion`（本文件同步加一节）。
2. 构建：`wails build -s -o Zen.exe -platform windows/amd64 -ldflags "-X github.com/irbis-sh/zen-desktop/internal/config.Version=vX.Y.Z -X github.com/irbis-sh/zen-desktop/internal/config.InstanceID=<保留原值> -X github.com/irbis-sh/zen-desktop/internal/config.NoSelfUpdate=true" -m -skipbindings -tags prod`（前端需先 `npm run build`）。
   **版本号必须用 `-ldflags` 注入**：`internal/config/config.go:28` 的 `Version` 默认值是 `"development"`，只改 `wails.json` 而不注入的话，运行时日志与"关于"页都会显示 development（2026-11-20 实测踩过）。
3. 备份现役 exe 到 `%LOCALAPPDATA%\Programs\Zen\backup\`，装机后用带 `-start` 启动并核对日志版本行。
4. 提交 + 打标签 `vX.Y.Z`；GitHub 推送需用户登录（见 `zen-plus发布计划.md` 第四节，当前仍未授权）。
