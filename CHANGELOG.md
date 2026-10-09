# Changelog

## v3.1.0

### 中文

zen-plus v3.1.0（仍基于上游 **v0.25.1** 基线，非上游合并版本）：27 个提交，四块内容——AdGuard 规则语义兼容、响应路径判定修复、引擎性能与健壮性强化、上游代理配置全面升级。

### 规则语义：更贴近 AdGuard 官方语义（B1-B8 兼容批次）
- 例外语义重构：逐请求取消、`$document` 页面级豁免、`$generichide`/`$elemhide`/`$jsinject` 等页面级修饰符真实生效；popup/$cookie/$script:inject 别名与 `$badfilter`/`$removeparam`/`$replace`/`$csp`/`$permissions` 批量语法补齐
- `$jsonprune` AdGuard 方言预处理（未加引号联合、`?(has/...)` 映射）+ 扩展路径（`[-]`/`{-}`/`\$..`/`[?()]`/`[=]`）+ 12 个 `trusted-*` 脚本注入键，激活一批此前解析失败或静默失效的规则（youtube/twitter/msn/pluto 等）
- 每批语法变更均以 83 万行真实语料逐行归因对拍验证（0 未归因）

### 响应路径判定修复（一批此前恒不生效的规则复活）
- `$xmlhttprequest`/`$xhr` 在响应路径改用 Fetch Metadata 判定：37 条存活规则（34 条 `$replace`）从此生效
- `$domain` 条件此前在响应侧恒返回 false：180 条响应动作规则（$cookie 78、$replace 79、$jsonprune 13、$csp 9）从此生效
- `$permissions` 按 AdGuard 语义仅作用于 frame 加载：修复 4255 次非 frame 响应被误加 Permissions-Policy 头（31 分钟捕获窗口内 100% 响应改写），并消除 +75µs/op 开销
- `$document` 异常作用域修复：某列表一条 `@@$doc,removeparam=...` 曾使全站脚本注入/元素隐藏失效（YouTube 广告回归的机制根因），现已按动作/查询修饰符正确收窄

### 引擎性能与健壮性
- 超长/组合 URL 遍历爆炸修复：68KB 对抗 URL 从分钟级 CPU＋183MB 内存降至 ~1ms／~0.7MB；1017 字符 `??` 组合 URL 从 15s+ 降至 8.4ms（全状态记忆化＋尾通配吞并＋4096 字符截断对齐 AdGuard）
- 同签名规则覆盖去重：默认列表裁剪 14,581 条被覆盖规则，live 堆 -3.3MB，33,303 URL 决策 0 差异
- match-all `$cookie`/`$removeparam` 动作索引：ModifyReq 352 → 241µs/op
- ModifyReq 热路径再提速 12.6%（180.3k → 157.6k ns/req，黄金对拍 133,324 决策 0 差异）
- 每请求分配：语料回放 413 → 7 次；hostmatch 自 TLD 向下建树（上游 #810）：matcher 内存 38MB → 6.8MB，asset 引擎 retained heap 42MB → 9MB
- 回滚开关：`ZEN_RULE_COVER_DEDUP=0`、`WithCoverageDedup(false)`、`WithActionIndex(false)` 均可恢复旧行为

### 上游代理增强
- 从 `upstream-proxy.txt` 文件迁移到应用内 设置 → 高级 配置（存 config.json，旧文件首次启动自动导入并改名 `.migrated`）
- 新增 HTTPS 与 SOCKS5 上游类型，支持可选凭证（HTTP(S) Basic 认证 / SOCKS5 RFC 1929）
- 目标主机名不在本地解析——DNS 留在上游；设置镜像到标准代理环境变量，过滤列表下载同样走链
- `ZEN_UPSTREAM_PROXY` 环境变量覆盖保留

### 出站协议重放（h2-mirror）
- 出站协议跟随客户端：HTTP/2 客户端按线上捕获的 h2 指纹参数重放（SETTINGS 值与顺序、连接级 WINDOW_UPDATE、PRIORITY 帧、伪头与头部顺序），HTTP/1.1 客户端保持 HTTP/1.1
- TLS 层仍为 ClientHello 结构镜像：出口 JA3 与直连逐字节一致的结论不变

### 其他
- 更新后重启重开窗口并恢复代理运行状态（上游 #799）
- 诊断装置（决策 tap、响应体转储、`ZEN_PPROF`/`ZEN_FORCE_H1_OUT`/`ZEN_LEGACY_OUT`）全部环境变量门控、默认关闭，不影响默认行为

### 诚实基线
- 以上性能数字来自各提交落地时的实测基准（提交前对拍验证），本次发布未重新全量复测
- 本版本**有意**让拦截行为更贴近 AdGuard 语义——v3.0.0 的"与上游逐请求一致"声明不再适用于本版本
- 已知测试问题：`internal/ruletree` 的 `TestP6EngineLongURLBoundedCost` 在冷启动单独运行时内存测量超 1MB 上限（实测 ~1.48MB，对抗路径时延仍 ~1ms、与修复前 183MB 相差两个数量级）；不影响二进制行为，待后续单独排查

### 安装
下载 Zen.exe 覆盖 `%LOCALAPPDATA%\Programs\Zen\Zen.exe`（配置与过滤器缓存通用）。SHA256 见下方 Assets。

---

### English

zen-plus v3.1.0 (still based on the upstream **v0.25.1** baseline — not an upstream merge): 27 commits in four areas — AdGuard rule-semantics compatibility, response-path condition fixes, engine performance and robustness, and a full upgrade of the upstream proxy configuration.

### Rule semantics: closer to official AdGuard semantics (batches B1–B8)
- Reworked exception semantics: per-request cancellation, `$document` page-scope exemptions, real enforcement of page-level modifiers (`$generichide`/`$elemhide`/`$jsinject`); popup/$cookie/$script:inject aliases plus `$badfilter`/`$removeparam`/`$replace`/`$csp`/`$permissions` syntax coverage
- `$jsonprune` AdGuard-dialect preprocessing (unquoted unions, `?(has/...)` mapping) + extended paths (`[-]`/`{-}`/`\$..`/`[?()]`/`[=]`) + 12 `trusted-*` scriptlet keys — activates rules that previously failed to parse or were silently inert (youtube/twitter/msn/pluto and others)
- Every syntax batch was verified with per-line attribution over an 833k-line real-corpus diff gate (0 unattributed)

### Response-path condition fixes (a class of dead rules revived)
- `$xmlhttprequest`/`$xhr` now use Fetch Metadata on the response path: 37 live rules (34 of them `$replace`) work again
- `$domain` unconditionally returned false on the response path: 180 response-action rules (cookie 78, replace 79, jsonprune 13, csp 9) work again
- `$permissions` now applies to frame loads only, per AdGuard docs: fixes 4,255 non-frame responses getting the Permissions-Policy header (100% of response rewrites in a 31-minute capture) and removes a +75µs/op overhead
- `$document` exception scoping fixed: one list's `@@$doc,removeparam=...` used to disable scriptlet/cosmetic injection site-wide (the mechanism behind the YouTube ad regression); exemptions are now correctly narrowed by action/query modifiers

### Engine performance and robustness
- Traversal-explosion fix for very long / combinatorial URLs: a 68KB adversarial URL went from minutes of CPU and 183MB to ~1ms / ~0.7MB; a 1017-char `??` combo URL from 15s+ to 8.4ms (full-state memoization + tail-subsumption + 4096-char truncation per AdGuard)
- Same-signature coverage dedup: 14,581 covered rules pruned on the default lists, 3.3MB live heap, zero decision diffs over 33,303 URLs
- Match-all `$cookie`/`$removeparam` action index: ModifyReq 352 → 241µs/op
- ModifyReq hot path another 12.6% faster (180.3k → 157.6k ns/req, golden master 133,324 decisions, 0 diffs)
- Per-request allocations: corpus replay 413 → 7; hostmatch trie keyed from the TLD down (upstream #810): matcher memory 38MB → 6.8MB, asset-engine retained heap 42MB → 9MB
- Rollback switches: `ZEN_RULE_COVER_DEDUP=0`, `WithCoverageDedup(false)`, `WithActionIndex(false)` restore the previous behavior

### Upstream proxy enhancements
- Moved from the `upstream-proxy.txt` file into the app (Settings → Advanced; stored in config.json, the legacy file is imported once on first start and renamed `.migrated`)
- New HTTPS and SOCKS5 upstream types with optional credentials (HTTP(S) Basic / SOCKS5 RFC 1929)
- Target hostnames stay unresolved locally — DNS happens at the upstream; the setting is mirrored into the standard proxy environment variables so filter-list downloads chain too
- The `ZEN_UPSTREAM_PROXY` environment variable override is preserved

### Outbound protocol replay (h2-mirror)
- The outbound protocol follows the client: an HTTP/2 client is re-originated over HTTP/2 from the fingerprint parameters captured on the wire (SETTINGS values and order, connection-level WINDOW_UPDATE, PRIORITY frames, pseudo-header and header order); an HTTP/1.1 client stays on HTTP/1.1
- The TLS layer remains the ClientHello structural mirror: JA3 through Zen stays byte-for-byte identical to a direct connection

### Other
- Updating now reopens the window and restores the proxy state after restart (upstream #799)
- Diagnostic tooling (decision tap, body dumps, `ZEN_PPROF`/`ZEN_FORCE_H1_OUT`/`ZEN_LEGACY_OUT`) is env-gated and default-off; no behavior change when disabled

### Honest baseline
- The performance numbers above come from the benchmarks measured when each commit landed (replay-verified before commit); this release did not re-run the full measurement suite
- This release **intentionally** moves blocking behavior closer to AdGuard semantics — v3.0.0's "request-by-request identical to upstream" statement no longer applies to this version
- Known test issue: `TestP6EngineLongURLBoundedCost` in `internal/ruletree` measures ~1.48MB/op on a cold single run against its 1MB guard (the adversarial path itself stays at ~1ms, two orders of magnitude away from the 183MB pre-fix state); binary behavior is unaffected, root cause to be investigated separately

### Install
Replace `%LOCALAPPDATA%\Programs\Zen\Zen.exe` (configuration and filter caches are shared). SHA256 under Assets.

## v0.25.0

### What's New

* **Xin chào!**
  Zen's UI is now available in Vietnamese. Thanks to @vuanhvu11982!
* **Fixed proxy start for non-admin macOS accounts**
  Pressing "Start" failed on standard (non-admin) macOS accounts with a "Command requires admin privileges" error, because `networksetup` refused to run without elevation. Zen now elevates these commands when needed, so starting the proxy prompts for admin approval instead of failing. Thanks to @stevehartwell for reporting the issue!
* **Linux installation script**
  A new `install.sh` script installs and uninstalls Zen across Linux distributions, automating the manual process of downloading and extracting a portable binary. See the README for usage. Thanks to @ohdonny!
* **Fixed Linux autostart**
  Zen was writing its autostart entry to `~/.config` instead of `~/.config/autostart`, so "Start on login" had no effect. The entry is now created in the correct directory, with a migration that fixes existing installs on upgrade. Thanks to @yofukashino!
* **More reliable filter list updates**
  Filter list fetching now survives slow or unreliable connections. Downloads are retried with backoff, updates are requested conditionally to save bandwidth, and Zen falls back to the last known-good list instead of losing protection when a fetch fails outright.
* **Friendly error page on failed page loads**
  Browser navigations that fail at the proxy now show a readable error page instead of a broken connection.
* **Proxy performance improvements**
  Long-lived connections (SSE, long polling) are no longer cut off by a fixed request timeout, idle connection limits were raised, and outbound connections now close cleanly on shutdown.
* **Other improvements**
  Faster process lookups on Linux via kernel Netlink queries instead of `/proc` parsing (thanks @LucasM4r!), and the Rules help button now points to Zen's new documentation site.

### New Contributors
* @vuanhvu11982 made their first contribution in https://github.com/irbis-sh/zen-desktop/pull/760
* @yofukashino made their first contribution in https://github.com/irbis-sh/zen-desktop/pull/769

Thank you for using Zen!

**Full Changelog**: https://github.com/irbis-sh/zen-desktop/compare/v0.24.1...v0.25.0

## v0.24.1

### What's New

* **Scriptlet fixes**
  Fixed a couple of bugs that stopped some scriptlet rules from working, which could let ads and trackers through on certain sites. The affected rules now behave as written.

Thank you for using Zen!

**Full Changelog**: https://github.com/irbis-sh/zen-desktop/compare/v0.24.0...v0.24.1

## v0.24.0

### What's New
* **Linux app starts again without `libayatana-appindicator3`**
  Since `v0.23.0`, Zen failed to launch on Linux systems that didn't have AppIndicator installed, quitting with a shared-library loading error. Zen now loads the system tray library at runtime instead of linking it at build time, so the app starts whether or not the library is present. If you ran into this after updating, see the troubleshooting guide: https://docs.irbis.sh/docs/zen/how-to/troubleshoot/#zen-wont-start-after-updating-to-v0230
* **`important` modifier**
  Zen now supports the `important` rule modifier. An important rule wins over regular exception rules, and only an important exception rule can cancel it. Thanks to @LucasM4r!
* **`ping` content-type modifier**
  Rules can now match hyperlink auditing (ping) requests with the `ping` content-type modifier. Thanks to @LucasM4r!
* **Noop modifiers**
  Zen now accepts noop modifiers (`_`) in rules and ignores them during parsing, which improves compatibility with filter lists. Thanks to @kamalovk!

### New Contributors
* @LucasM4r made their first contribution in https://github.com/irbis-sh/zen-desktop/pull/726

Thank you for using Zen!

**Full Changelog**: https://github.com/irbis-sh/zen-desktop/compare/v0.23.0...v0.24.0

## v0.23.0

### What's New

* **Linux system tray support**
  Zen now sits in the system tray on Linux in supported desktop environments. Closing the window minimizes Zen to the tray and keeps the proxy running instead of shutting it down. Thanks to @ohdonny!
* **こんにちは！**
  Zen's UI is now available in Japanese, thanks to @AI-Avalon!
* **WebSocket filtering**
  WebSocket traffic can now be matched and blocked. Additionally, Zen now recognizes the `$websocket` content type modifier. Thanks to @inflame-ue!
* **More reliable content-type matching**
  Content-type rules now match against the `Content-Type` response header, not only the `Sec-Fetch-Dest` request header. These rules now work more consistently, especially with non-browser apps. Thanks to @Mirwinli!
* **Separate PAC port setting**
  Settings > Advanced now has a field for the PAC proxy port. Useful for apps that do not respect system proxy settings automatically and need to be configured to use Zen manually.
* **Other improvements**
  Tightened the scope of generated certificates (thanks @inflame-ue!), fixed a resource leak when the proxy fails to start (thanks @Mirwinli!), improved filtering accuracy (thanks @Mirwinli!), and made other internal improvements to the filtering engine.

### New Contributors
* @Mirwinli made their first contribution in https://github.com/irbis-sh/zen-desktop/pull/700
* @AI-Avalon made their first contribution in https://github.com/irbis-sh/zen-desktop/pull/696
* @inflame-ue made their first contribution in https://github.com/irbis-sh/zen-desktop/pull/717

Thank you for using Zen!

**Full Changelog**: https://github.com/irbis-sh/zen-desktop/compare/v0.22.0...v0.23.0

## v0.22.0

### What's New

* **Regular-expression rule support**
  Zen now supports regexp rules. This improves compatibility with filter lists that use regexp patterns to match dynamic tracking and ad URLs.
* **Fixed allowlisting for hosts rules**
  The "Allow" button on "Blocked by Zen" pages now works correctly when the block was caused by a hosts-style rule such as `0.0.0.0 example.net`.
* **Fixed macOS service compatibility**
  HTTPS exclusions were updated to fix sign-in and calling issues with Apple Messages and FaceTime. Thanks to @rishiskhare for reporting the issue!
* **Removed a stale built-in filter lists**
  An outdated Polish anti-adblock list has been removed from the configuration. Thanks to @qorexdevs for the contribution!
* **UI polish**
  The header and filter lists now use cleaner divider styling for a more consistent look.

### New Contributors
* @qorexdevs made their first contribution in https://github.com/irbis-sh/zen-desktop/pull/682

Thank you for using Zen!

**Full changelog**: https://github.com/irbis-sh/zen-desktop/compare/v0.21.1...v0.22.0

## v0.21.1

### What's New

This is a hotfix for `0.21.0`, which had an incorrect version number in the file manifest.

**Full Changelog**: https://github.com/irbis-sh/zen-desktop/compare/v0.21.0...v0.21.1

## v0.21.0

### What's New
- **App allow- and block-listing**
  The new **"App routing"** setting gives you the ability to choose which apps should use Zen's proxy. It gives you the ability to exclude a developer tool which has trouble with proxying, a gaming app where you want maximum performance, or a browser with built-in ad-blocking.
- Other minor improvements.

**Full Changelog**: https://github.com/irbis-sh/zen-desktop/compare/v0.20.0...v0.21.0

## v0.20.0

### What's new
- **Process info in request logs**
  Request logs now show information about processes which initiated the request. This makes it easier to trace activity and understand which applications are responsible for specific traffic.
- **Merhaba!**
  Zen now speaks Turkish, thanks to @Wek1d! Want to contribute a new language or improve an existing one? Check out our contributing guidelines.
- More minor improvements.

### New contributors
- @Wek1d made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/641

**Full changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.19.2...v0.20.0

## v0.19.2

### What's New
- __Faster request matching__
  Optimised a bottleneck in the request matching engine that accounted for 10-15% of overall CPU usage. Browsing should feel a little snappier across the board.
- __macOS network permissions__
  Improved compatibility with the "Require an administrator password to access system-wide settings" option in macOS. Zen now correctly elevates privileged commands instead of failing.
- __macOS login item naming__
  Fixed macOS login items displaying the developer name instead of the app name – thanks to @ganeshmshetty!

### New Contributors
- @ganeshmshetty made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/627

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.19.1...v0.19.2

## v0.19.1

### What's New

This is a hotfix to v0.19.0.

- __Steam fix__
  This release fixes the "NO CONNECTION" issue in Steam.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.19.0...v0.19.1

## v0.19.0

### What's New

This is a performance-focused update with many improvements across the stack.

- Zen's proxy now speaks **HTTP/2** in addition to HTTP/1.1, for both inbound and outbound traffic. Thanks to @brycewray for initiating the conversation on this.
- The rule matching engine is **2x faster on average**, with more than **200x improvement on long URLs** (1,000+ characters). This particularly affected Google Meet and Google Chat – expect much improved loading times on these services.
- Extended CSS rule application now optimizes `:has`, `:not`, and `:is` pseudo-class evaluation when it can be run natively. Thanks to @krystian3w for advice along the way.
- Other minor improvements and bug fixes.

### New Contributors
- @LinaKACI-pro made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/603

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.18.1...v0.19.0

## v0.18.1

### What's New
- __Fixed self-updates__
  Self-updates were failing for some users due to a 20-second timeout that sometimes wasn't enough to download the update over GitHub's CDN. The timeout has been increased to fix this.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.18.0...v0.18.1

## v0.18.0

### What's New
- __no-doomscroll__
  Zen now ships with no-doomscroll – a set of filter lists that remove infinite feeds from social media websites, letting you use them without getting pulled into endless scrolling. You can enable them under Filter lists / Digital wellbeing. Learn more on the [no-doomscroll homepage](https://github.com/ZenPrivacy/filter-lists/blob/master/no-doomscroll/readme.md).
- Other minor improvements.

Thank you for using Zen!

__Full Changelog__: https://github.com/ZenPrivacy/zen-desktop/compare/v0.17.0...v0.18.0

## v0.17.0

### What's New

- __Support for the `!#include` directive in filter lists__
  This improves compatibility with a wider range of filter lists, including regional ones.
- __`:style()` extended CSS pseudo-class support__
  Zen is now even better at hiding unwanted elements on webpages.
- __Improved cache behavior__
  Injected page assets are no longer cached by browsers, so pages update instantly when you change the configuration or turn Zen off.
- __Improved Linux support__
  Added partial support for XFCE – thanks to @xoxorwr!
- Other bug fixes and performance improvements.

### New Contributors

- @xoxorwr made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/558

Thank you for using Zen!

__Full Changelog__: https://github.com/ZenPrivacy/zen-desktop/compare/v0.16.0...v0.17.0

## v0.16.0

### What's New
- __Performance improvements__
  We've optimized our filtering engine, with significant performance gains on long URLs.
- __Better UI__
  The application now includes a new logo and a new font, improving visual consistency across platforms, along with other minor UI refinements.
- __Better injection reliability__
  Improved handling of Content Security Policy (CSP) ensures that injections work more reliably across a wider range of sites. Thanks to @kasyap1234 for the contribution!
- __NSS trust store__
  The app now also installs the CA certificate into the NSS trust store. This notably improves the experience on Firefox – you're now less likely to encounter certificate errors on first launch. Thanks @donnykd for implementing this!
- __More reliable proxy on macOS__
  We've improved how the system proxy is configured on macOS, ensuring filtering remains active across all network interfaces. This also improves compatibility with various VPNs.
- Other minor improvements and bug fixes.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.15.4...v0.16.0

## v0.15.4

### What's New
- __File downloads fix__
  Fixed an issue that caused downloaded files to become corrupted. Thanks to @rugabunda for reporting it.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.15.3...v0.15.4

## v0.15.3

### What's New
- __Windows uninstaller fix__
  The uninstaller now automatically terminates any running instances of Zen, ensuring a complete removal.
- Other minor bug fixes.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.15.2...v0.15.3

## v0.15.2

### What's New
- **Hosts rules fix**
  Host style rules are now working as intended.
- Other minor bugfixes.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.15.1...v0.15.2

## v0.15.1

### What's New
This release fixes the "myRules is nil" error experienced during application startup. We apologize for the inconvenience.

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.15.0...v0.15.1

## v0.15.0

### What's New
- __Salut!__
  Zen now speaks French, thanks to @Armitryx! Want to contribute a new language or improve an existing one? Check out our contributing guidelines.
- Bug fixes and minor improvements.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.14.0...v0.15.0

## v0.14.0

### What's New

- **你好！**
  Zen now speaks Traditional Chinese, thanks to @lynda0214! Want to contribute a new language or improve an existing one? Check out our contributing guidelines.
- Minor stability improvements and bug fixes.

### New Contributors

- @lynda0214 made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/485

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.13.1...v0.14.0

## v0.13.1

### What's New

- **Default update policy fix**
  The default update policy is now set to automatic, ensuring users automatically receive the latest updates by default.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.13.0...v0.13.1

## v0.13.0

### What's New

- **Background update checks**
  Zen now periodically checks for updates in the background, ensuring you get the latest updates faster.
- **Interactive onboarding**
  A new smooth onboarding experience walks you through the initial app setup. Thanks to @kamalovk!
- **Memory optimization**
  Rules are now stored more efficiently, reducing the overall RAM usage. Thank you @lzap for help with the improvements!
- **KDE system proxy support**
  Zen now properly sets system proxy settings on KDE, which improves integration with the desktop environment. Thanks to @donnykd for implementing this feature!
- **Ciao!**
  Zen now speaks Italian, thanks to @davide-damico! Want to contribute a new language or improve an existing one? Check out our contributing guidelines.
- **More MITM exclusions**
  Thanks to @Speedauge, Zen now excludes more traffic from sensitive websites from proxying, improving the overall security of your system.
- **Extended CSS improvements**
  We're continuing to work on our extended CSS engine, bringing more stability and features.
- More minor improvements and bug fixes.

### New Contributors

- @davide-damico made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/454
- @lzap made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/458
- @Speedauge made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/481

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.12.0...v0.13.0

## v0.12.0

### What's New

- **Extended CSS**
  Zen now supports extended (procedural) CSS, enabling more powerful and comprehensive visual content blocking.
- **Block page**
  When a web page request is blocked, Zen now displays a dedicated block page, making it easier to review and unblock rules if needed.
- **CSP improvements**
  Fixed issues with Content Security Policy (CSP) modification and added support for patching inline styles, improving site compatibility while keeping everything secure.
- **macOS autostart fix**
  Resolved an issue where Zen's filtering wouldn't start automatically on macOS when launched at login.
- **Safer archive handling**
  Zen now uses Go's built-in security features to handle ZIP and TAR files more safely during updates, reducing the risk of path traversal issues. Thanks to @donnykd!
- Other bug fixes and small improvements.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.11.3...v0.12.0

## v0.11.3

### What's New

- **Fixed "Phishing URL Blocklist" format**
  Resolved an issue with the **Phishing URL Blocklist** that caused overly aggressive request blocking. Zen should now behave correctly when this list is enabled.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.11.2...v0.11.3

## v0.11.2

### What's New

- **Reduced memory consumption**
  Zen now uses about 10–15% less memory, thanks to work by @ilovelinabell.
- **你好！**
  Zen now speaks Chinese, thanks to @ccarstens! Want to contribute a new language or improve an existing one? Check out our contributing guidelines.
- **Start/stop button hotkey**
  You can now press **Space** to quickly start and stop the proxy. Thank you, @donnykd, for the contribution!
- **Single instance locking**
  Zen now keeps only a single instance of the application active, ensuring that it doesn't clutter your desktop. Thanks to @kasyap1234!
- **Windows binary signing**
  With the help of SignPath, Zen's Windows binaries are now signed with a certificate. Expect no more warnings during installation and fewer false antivirus flags.
- **Filter list buttons**
  You can now quickly copy and open filter lists in your browser. Thanks to @RustemMT for the contribution.
- Bug fixes and other small improvements.

### New Contributors

- @ccarstens made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/373
- @donnykd made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/372
- @ilovelinabell made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/350
- @kasyap1234 made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/381
- @BUTTER-BEAR made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/389
- @RustemMT made their first contribution in https://github.com/ZenPrivacy/zen-desktop/pull/397

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.11.1...v0.11.2

## v0.11.1

### What's New

- **CSP injection fix**
  Fixed an issue with script injection on some websites using `unsafe-inline` in their Content Security Policy. Zen now correctly avoids using `nonce`-based injection when it's incompatible, restoring functionality on affected sites.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.11.0...v0.11.1

## v0.11.0

### What's New

- **Clipboard sanitization**
  Some websites add tracking parameters (like utm_source and si) to URLs copied via their "Share" buttons. Zen now strips these trackers for your privacy. No more sneaky tracking when sharing content with your friends.
- **More reliable scriptlets injection**
  Zen can now inject scriptlets on more sites — including those with strict Content Security Policies (CSP) — ensuring our protections stay active without compromising your security.
- **Fixes to regional filter lists**
  Fixed a broken link to RU AdList and replaced the discontinued Icelandic filter list with its maintained Brave version.
- Bug fixes and other improvements.

Thank you for using Zen!

**Full Changelog**: https://github.com/ZenPrivacy/zen-desktop/compare/v0.10.1...v0.11.0

## v0.10.1

### What's New

- **Fixed Windows installer**:
  The Windows installer for the version 0.10.0 has unfortunately included the version of the app with its self-update capabilities disabled. If you're on Windows, not getting the prompt for this update, and missing the "Choose how updates are installed" option in the settings, please manually uninstall the app and download a newer version. We apologize for the inconvenience.
- **Proxy exclusions**:
  The list of proxy exclusions now includes more hosts. This fixes error with Apple Pay on macOS, the desktop ChatGPT app, and improves your security overall.
- **Homebrew**:
  The macOS app is now available on Homebrew. Go to our homepage, zenprivacy.net, to get the installation instructions.

### New Contributors

- @michaelthatsit made their first contribution in <https://github.com/ZenPrivacy/zen-desktop/pull/352>

Thank you for using Zen!

**Full Changelog**: <https://github.com/ZenPrivacy/zen-desktop/compare/v0.10.0...v0.10.1>

## v0.10.0

### What's New

- **Stronger content-blocking engine**

  - New rule modifiers: `jsonprune` and `remove-js-constant` enable detection-free YouTube ad-blocking.
  - New scriptlets: `prevent-setTimeout`, `prevent-setInterval`, `prevent-addEventListener`, `no-topics`, and `no-protected-audience`.
  - Improved filter list handling with on-disk caching and minor correctness/performance enhancements.

- **Fresh Light theme**
  Switch between dark and light modes to match your desktop or daylight.

- **Expanded language support**
  Сәлем! Hallo! Zen now speaks **Kazakh** and **German**. Want to contribute a new language or improve an existing one? Check out our contributing guidelines.

- **Enhanced security & supply chain hardening**

  - Automatic removal of Zen's root CA on Windows during uninstallation.
  - Build artifact attestation for improved supply-chain security.

- **UI & UX improvements**
  Donate button, a quick link to the changelog, disabled controls when the proxy is active, and more.

### New Contributors

- **@pulkitgarg04** – updated Hungarian filter list URL
- **@colinfrerichs** – config updates for v0.10.0

**Full Changelog**: [github.com/ZenPrivacy/zen-desktop/compare/v0.9.0...v0.10.0](https://github.com/ZenPrivacy/zen-desktop/compare/v0.9.0...v0.10.0)

## v0.9.0

### What's New

- **Multi-language support**: Zen now supports multiple languages, with more on the way. You can switch your preferred language in the settings. Huge thanks to @kamalovk for laying the groundwork for this feature.
- **Background self-updates**: Zen can now check for and apply updates automatically in the background at startup. You can enable this behavior in the settings.
- **Minimized startup**: When autostart is enabled on Windows, Zen now launches minimized to the system tray - keeping things quiet until you need them. Thanks to @Zanphar for the suggestion.
- **Scriptlet enhancements**: Numerous improvements to scriptlets, including new additions and stability upgrades to existing ones.
- **Internal filtering engine improvements**: The filtering engine now supports precise exceptions, which allows for more unwanted content to be blocked.
- **Higher resolution icons on Windows**: Zen now features sharper, high-resolution icons on Windows, thanks to @TobseF.
- **ARM64 builds for Linux**: Native ARM64 builds are now available for Linux users.
- **System proxy configuration via PAC**: Zen now configures the system proxy using a PAC file, resolving issues with networking in built-in Windows apps and improving overall security.
- **Join our Discord community**: We've launched a Discord server! Come say hi, share tips, and stay up to date with the latest on Zen: <https://discord.gg/jSzEwby7JY>. You'll also find the link on our website: <https://zenprivacy.net>.

### New Contributors

- @kamalovk made their first contribution: <https://github.com/ZenPrivacy/zen-desktop/pull/269>
- @TobseF made their first contribution: <https://github.com/ZenPrivacy/zen-desktop/pull/267>

**Full Changelog**: <https://github.com/ZenPrivacy/zen-desktop/compare/v0.8.0...v0.9.0>

## v0.8.0

### What's New

- **Performance Improvements**: We rewrote our proxy so that it no longer waits for the entire response before starting to pass data to the browser. Expect 1.5–2× improvements in page download times.
- Minor enhancements to content blocking and privacy preservation.

Thank you for using Zen!

**Full Changelog**: <https://github.com/ZenPrivacy/zen-desktop/compare/v0.7.2...v0.8.0>

## v0.7.2

### What's New

- **Character Encoding Fix**: Improved character encoding detection to handle websites with non-standard encodings more gracefully. Many thanks to @2372281891 for reporting the issue.

Thank you for using Zen!

**Full Changelog**: <https://github.com/ZenPrivacy/zen-desktop/compare/v0.7.1...v0.7.2>

## v0.7.1

### What's New

- **Navigator API Bug Fix**: Resolved a critical issue that impacted the stability of websites using the Navigator API.

Thank you for using Zen!

**Full Changelog**: <https://github.com/ZenPrivacy/zen-desktop/compare/v0.7.0...v0.7.1>

## v0.7.0

### What's New

- **Cosmetic Filtering**: Annoying and intrusive elements on webpages are now automatically blocked for a cleaner browsing experience.
- **JavaScript Rule Injection**: JS rules expand on scriptlets and offer advanced ad-blocking and privacy-preserving capabilities in the most complex cases.
- **Windows System Tray Icon Stability**: Resolved an issue where the tray icon could become unresponsive after prolonged use on Windows.
- Various stability improvements and bug fixes.

Happy 2025 and thank you for using Zen!

**Full Changelog**: <https://github.com/ZenPrivacy/zen-desktop/compare/v0.6.1...v0.7.0>

## v0.6.0

### What's New

- **Scriptlets**: Introducing scriptlets—advanced ad-blocking tool designed to handle cases where regular filtering is insufficient.
  - **First-Party Self-Update**: We've completely rewritten our self-updating system for improved stability. Future macOS updates will now be delivered seamlessly without requiring a reinstallation of the app. Special thanks to @AitakattaSora for implementing this feature.
  - **Custom Filter List Backup**: Advanced users can now easily back up and restore their custom filter lists. Many thanks to @Noahnut for your contribution.
  - **Rules Editor**: A new tab in the app allows you to add custom filter rules directly inside the app.
  - **Export Application Logs**: Logs are now written to disk, making it easier for the development team to diagnose and resolve issues. Thank you to @AitakattaSora for implementing this feature.
  - **Improved Linux Support**: The app now starts without errors on non-GNOME systems. You can now manually configure the HTTP proxy on a per-app basis if needed. Thanks to @AitakattaSora for this enhancement.
  - **Improved Windows Support**: The app now shuts down gracefully and resets the system proxy during system shutdown, preventing internet disruptions at startup.
  - Various stability improvements and bug fixes.

Warning: On macOS, the app will not function properly after the update. Please visit our homepage, [zenprivacy.net](https://zenprivacy.net), to manually download the latest version. Future updates will be delivered seamlessly.

Thank you for using Zen!

**Full Changelog**: <https://github.com/ZenPrivacy/zen-desktop/compare/v0.5.0...v0.6.0>
