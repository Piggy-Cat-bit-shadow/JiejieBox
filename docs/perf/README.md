# Performance reports

Stage 1 of the performance work: the laboratory, what it found, and what it
could not measure.

| document | answers |
|---|---|
| [ASSET-INVENTORY.md](ASSET-INVENTORY.md) | what measurement already existed for each of the eight protocols, and what was missing |
| [ENVIRONMENT.md](ENVIRONMENT.md) | the exact machine, toolchain, tags and capability checks behind every number |
| [BASELINE.md](BASELINE.md) | the current baseline: method, headline numbers, and how to read them |
| [FAILURES.md](FAILURES.md) | blocked cells, absent tools, and the defects the lab found in itself |
| [matrix.md](matrix.md) | generated tables for every cell in the matrix (no hand-copied numbers) |
| [STAGE-1-REPORT.md](STAGE-1-REPORT.md) | the stage report in the requested order |

The operating manual for the laboratory itself is
[`scripts/perf/README.md`](../../scripts/perf/README.md).

## Rules these documents follow

1. A number is attached to a commit, a binary tag set, a host and a network
   path. A number without those is not reused.
2. `null` means not measured. It is never rendered as zero.
3. Different transport guarantees are not ranked against each other. QUIC
   DATAGRAM, a reliable stream carrying datagrams, and a userspace IP tunnel
   answer different questions.
4. An old report's figures are never presented as this baseline. Where history
   is mentioned it is labelled as history.
