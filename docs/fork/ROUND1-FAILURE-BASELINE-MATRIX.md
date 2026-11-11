# Round 1 失败基线矩阵（A/B/C/D）

**结论：sing-tun race 的三类失败、Windows 三个包、go vet 警告均为合并前已存在的基线；httpclient 的失败是负载敏感的既有 flake。没有发现由本轮 merge 引入的可复现回归。完整 sing-tun `-race` 不能记为 PASS。**

环境：Windows 11 / WSL Ubuntu-24.04；Go 1.25.5；sing-tun `unshare -Urn`；同一 kernel、CPU/内存、Go、GOMODCACHE、race 开关、`-run` 集合、600s 外层超时。
对照源码：合并前 `57e9d17fa852`，合并后 `99abf3c27666`（两者测试文件同名同内容）。

## 1. sing-tun A/B/C/D

命令（每轮独立，`unshare -Urn`，`-run '^(TestGoKernelSACKReneging|TestGoKernelSequenceWrap|TestGoMemoryDial)$'`）：

| 轮次 | 源码 | 模式 | 退出码 | 失败 |
|---|---|---|---|---|
| A | 57e9d17 | plain | 1 | 仅 `TestGoMemoryDial/ipv6=false/unreachable`（context deadline） |
| B | 99abf3c | plain | 1 | 同一用例，同一错误 |
| C | 57e9d17 | `-race` | 1 | `SACKReneging/ipv6=false`（SACK timeout）；`SequenceWrap` 6 个 mode 全部 60s I/O timeout；`MemoryDial/unreachable` |
| D | 99abf3c | `-race` | 1 | `SACKReneging/ipv6=true`（同 SACK timeout，另一族）；`SequenceWrap` 6 个 mode 全部 60s I/O timeout；`MemoryDial/unreachable` |

原始日志：

- `audit-evidence/upstream/round1-race/old-plain.txt`
- `audit-evidence/upstream/round1-race/old-race.txt`
- `audit-evidence/upstream/round1-race/new-plain.txt`
- `audit-evidence/upstream/round1-race/new-race.txt`
- `audit-evidence/upstream/round1-race/run.log`（各轮耗时）

### 归因

1. **plain 模式**：两版都只有 `unreachable` 失败。该用例在 userns 中收不到预期 ICMP unreachable，返回 `context deadline exceeded`；这是环境限制，不是代码回归。
2. **race 模式**：两版都失败，且失败类别相同（SACK 超时、SequenceWrap 的 32-bit wrap I/O 超时、unreachable）。差别只是 SACK 在旧版命中 IPv4、新版命中 IPv6，属于 race 时序下的族选择差异，不是“新版新增失败”。
3. `SequenceWrap` 的 6 个 mode 在两版 race 下全部在测试自身的 60s 上限处超时；plain 模式两版都通过。因此这是 **race instrumentation + userns/Go stack 在该测试负载下的进度超时**，不是本轮引入。
4. 未通过调大 timeout/删测试来“修复”；保留测试和真实日志。

**sing-tun 完整 `-race`：NOT PASS（BASELINE_CONFIRMED）**；专项 `SACKReneging/SequenceWrap` 在 plain 下通过，race 下两版同败。

## 2. Windows 三个包

| 包 | 失败测试 | 合并前 4810371 | 最终 4ec0dec | 根因 |
|---|---|---|---|---|
| common/tls | `TestWindowsClientHandshakeTLS13`, `TestWindowsClientRoundtripTLS13`, `TestWindowsClientTLS13PostHandshakeConcurrentWrite` | 同名失败（`tls handshake: read handshake: unexpected EOF`） | 同名失败 | 测试环境/Windows TLS 握手，非 merge |
| common/tlsspoof | `TestIntegrationSpooferOpenClose`, `TestIntegrationConnInjectsThenForwardsRealCH`, `TestIntegrationSpooferInjectThenWrite` | 同名失败（`windivert: open SCM: Access is denied`） | 同名失败 | 需要管理员/WinDivert 驱动 |
| common/windivert | `TestIntegrationTamperedCacheRepaired`, `TestIntegrationOpenSendOnly`, `TestIntegrationCloseTwice`, `TestIntegrationRecvAbortsOnClose`, `TestIntegrationConcurrentOpen` | 同名失败（SCM Access denied） | 同名失败 | 需要管理员/WinDivert 驱动 |

证据：`audit-evidence/upstream/round1-premerge-windows.txt` 与 `round1` 前全量日志 `final-windows-full.txt`。

**结论：BASELINE_CONFIRMED。** 未用 `-vet=off` 或跳过这些测试制造“全绿”；全量报告仍明确列出 3 个包失败。

## 3. common/httpclient 负载敏感失败

- 失败测试：`TestRaceSimultaneousCompletionsDeliverExactlyOneResponse`，断言 `transport.broken` 应为 0，实际为 1。
- 原始日志：`final-windows-full.txt`（全量 `-p 1` 下的 1 次失败）。
- 代码对照：`git diff --stat 48103716845e..HEAD -- common/httpclient` **为空**，测试与实现均未被本轮 merge 改动。
- 复跑：该测试单独 `-count=20`（普通）与 `-count=5`（`-race`）全部 PASS；全量并发下仅出现过 1 次。
- 触发条件：全量多包并发/CPU 竞争下，H3 竞速取消结果的时序窗口；属于既有测试的负载敏感 flake，不是本轮 data race 回归。

**结论：BASELINE_CONFIRMED（负载敏感）**，不把“单独 5/5”写成全量通过；全量日志保留。

## 4. go vet unsafe.Pointer

三处，均与合并前一致（文件未被本轮修改）：

| 文件:行 | 合并前 | 最终 | 分类 |
|---|---|---|---|
| `daemon/managed_service.go:75` | 同名警告 | 同名 | Windows 服务回调/ABI 层，既有 |
| `experimental/libbox/debug.go:11` | 同名警告 | 同名 | gomobile/Windows 调试边界，既有 |
| `experimental/boxdd/authenticode_windows.go:220` | 同名警告 | 同名 | Authenticode/Windows API 边界，既有 |

证据：`audit-evidence/upstream/round1-govet.txt`；旧工作树 `go vet` 输出与之相同。**分类：BASELINE_CONFIRMED，非合并引入，本轮不做大范围重构。**

## 5. 未完成/边界

- `SequenceWrap` 的 race 超时未做根因级优化（可能涉及 race instrumentation 下 Go stack 的吞吐）；本轮只证明合并前后同败，保留 `NOT_VERIFIED` 的性能结论。
- 未在真实 macOS/Apple 设备运行；见 `ROUND1-AUDIT-CLOSURE.md` 的 P1-03。
