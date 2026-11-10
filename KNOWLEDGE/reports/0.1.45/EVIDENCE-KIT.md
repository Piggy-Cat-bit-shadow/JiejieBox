# 证据工具包（不入库，按需重建）

本归档引用了一套采样与判定工具。工具源码、原始样本与批次产物保存在本机证据卷（`<evidence>`），不属于本仓库；为避免"报告可读、证据不可复核"，这里记录其接口、源码哈希与重建方式。

## 文件与哈希（SHA256）

| 文件 | 作用 | 源码 SHA256 |
|---|---|---|
| `d6tool/main.go` | 采样器与判定器（`sample` / `validate` / `gate`，Windows，`golang.org/x/sys`） | `ac60057caa60439384abbc5e0a7d5fff2467e7f4895f5aefb34890b13521b8c2` |
| `d6tool/selftest.go` | 判定规则自检（10 条合成用例） | `66cab602b72dbfa740cf6dbbfcabb31bc488b0f9c64735891c4a1954ec39c3df` |
| `d6tool/go.mod` | 模块与依赖（`golang.org/x/sys v0.47.0`） | `e4a89829e2dc2af46582925e87970e667633ac6f068f6f0f5325215e47fc49e9` |
| `run_d6.ps1` | 单批执行：门禁、6+6 轮、逐轮判定、三个独立断言 | `4dcb672106531762302b3c98a93394dc4339beeb81f7cbaa0734e96b426bbe5a` |
| `run_d6_wait.ps1` | 连续门禁等待器（全批优先） | `f48ab4a2d825517099c2725618e231cafbac1e09bd6ad1b096e18e281f25df54` |
| `analyze_d6.py` | 批次分析（只采信 valid 轮，重新推导可用性） | `cf87b0286074611e34d37438273a4e1bc1d16b928b0fb44852a6ea427170aa9a` |

## 重建

```text
cd <evidence>/d6tool
go build -o d6tool.exe .
d6tool.exe selftest            # 期望 10/10 通过
d6tool.exe gate -samples <samples.jsonl> -window 30s
d6tool.exe validate -samples <samples.jsonl> -round <round.json> -out <verdict.json>
```

- 采样器每 100 ms 记录全机 `GetSystemTimes` 与每进程累计 CPU/RSS/PID/创建时间；判定使用"实际 CPU 增量 ÷ 实际采样跨度 ÷ 逻辑核数"，按 PID 与创建时间排除被测进程与采样器。
- 判据：开跑前 30 个 1 秒样本平均全机负载 <5%，且任一其他进程归一化占用 ≤2%；每轮另需退出码 0、CSV 行数与成功数均为 321、零延期/失败/取消/超时、样本覆盖 ≥80% 且无 >3 个采样间隔的空洞。
- 三个断言相互独立：`testCompleted`、`environmentQualified`、`usableForComparison`。

## 原始数据位置

- 采样批次：`<evidence>/d6/batch-*/`（`samples/`、`rounds/`、`verdicts/`、`batch.json`、`analysis.json`）。
- 连续门禁：`<evidence>/d6_wait.log`（每次门禁的均值、最忙进程、合计、结论）。
- d5 低负载对照：`<evidence>/d5_*`（12 个 CSV、12 个日志、`d5_rounds.json`、`d5_analysis.*`）。
