# Round 1 远端与 CI 证据（最后一次采集）

采集时间：2026-11-11T12:19:25+08:00（`ls-remote`）；CI 查询 2026-11-11T12:14+08:00。
原始输出：`audit-evidence/upstream/round1-final-lsremote.txt`、`round1-github-compare.txt`。

## 1. 主仓远端 heads

```text
ecfffe7d06915d5ad5cfef771b39ac73a72696f2	refs/heads/backup/02-ecfffe7d0
75f608074090dc56d39439f137bbf80a90438b8f	refs/heads/feature/proxy-group-hidden
40dfed27474eaf8d1219fc3bb13e17015c06e5fe	refs/heads/integration/0.1.45-final
40dfed27474eaf8d1219fc3bb13e17015c06e5fe	refs/heads/testing
```

说明：

- `backup/02-ecfffe7d0` 是从 archive tag 恢复的活跃并行线，不是本轮产品分支；见 CLEANUP。
- `feature/proxy-group-hidden` 是其他工作的活跃分支，未 checkout/未推送。
- `testing` 与 `integration/0.1.45-final` 同点 `40dfed2`。

## 2. 主仓 Git 图

```text
HEAD = 40dfed27474eaf8d1219fc3bb13e17015c06e5fe
pre-merge merge-base = 8eb4395a391b775ad240433a7009f39357c1eba0
merge = 303fbb47dfdca4520dd201fea8bd48b3db1f1010
merge parents = 48103716845e4ac00cc5d7eb0aa36febd90b477c
                 a13dbe1f785f66c86acbf3985c33fdafea0cbfd6
final merge-base = a13dbe1f785f66c86acbf3985c33fdafea0cbfd6
upstream = SagerNet/sing-box:testing @ a13dbe1f785f66c86acbf3985c33fdafea0cbfd6
ahead/behind = 3738 / 0
```

## 3. CI（真实 run，按 head_sha 查询）

| workflow | run | 结果 | head_sha | URL |
|---|---|---|---|---|
| Verify | 42955409204 | success | `40dfed27474eaf8d1219fc3bb13e17015c06e5fe` | https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42955409204 |
| Android Core ARM64 | 42955409234 | success | `40dfed27474eaf8d1219fc3bb13e17015c06e5fe` | https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42955409234 |
| Mobile contract | 42942954909 | success | `08b52a30b4dbc51fdfbf1ed325e6f571c05351e7` | https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42942954909 |

补充：

- `b473c21` 代码树另有 Verify `42945339834` / Android `42945339735` success。
- `2528511` 审计 commit 另有 Verify `42953972668` / Android `42953972663` success。
- `40df` 是 2528511 之后 6 个文件（文档/审计 JSON）的 docs 提交；Verify/Android 已在该 HEAD 上重新成功，不是引用旧 run。

## 4. GitHub Compare

原始记录 `audit-evidence/upstream/round1-github-compare.txt`，查询时间 `2026-11-11T11:34:50+08:00`，方向 `upstream...fork`：

```text
main       ahead_by=0 behind_by=3736   <- 该记录早于 40df，仅作方向/目标核验
sing       ahead_by=0 behind_by=65
sing-tun   ahead_by=0 behind_by=183
sing-quic  ahead_by=0 behind_by=11
quic-go    ahead_by=0 behind_by=20
utls       ahead_by=0 behind_by=69
android    ahead_by=0 behind_by=15
desktop    ahead_by=0 behind_by=42
dashboard  ahead_by=0 behind_by=10
apple      ahead_by=0 behind_by=220
```

注意：`3736` 是 11:34 的**历史快照**；当前 `40df` 为 3738/0（本地 `rev-list`，2026-11-11T12:19）。`ahead_by=0` 的含义是“上游没有 fork 缺少的提交”，方向不能被解释成 Fork 领先数。

## 5. source-lock / gate

```text
python scripts/ci/release-freeze.py verify --lock release/source-lock.json
PASS: every pin in the tree is the pin the lock records.

python scripts/ci/check-round1-audit.py
PASS: round-1 audit facts are consistent

python scripts/ci/check-round1-audit.py --self-test
SELFTEST PASS: all injected inconsistencies were rejected
```

原始输出：`evidence/source-lock-verify.txt`、`evidence/round1-consistency-gate.txt`。
