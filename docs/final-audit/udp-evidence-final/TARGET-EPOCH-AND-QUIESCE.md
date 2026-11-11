# Target epoch and quiesce

## Epoch isolation

The 20-byte UDP header already carries `magic=PLB1` and an epoch. The target now
parses it and keeps `EpochDatagrams[epoch]` / `EpochEchoed[epoch]` in addition to
the global counters (`internal/target/target.go`). The map is bounded by the
number of epochs a phase uses (normally one measured + one warmup) and is cleared
by `Reset()`.

The runner reads only `stats.EpochDatagrams[measuredEpoch]` after the run. Warmup
(`epoch ^ 0x9e3779b9`), previous reps and any late datagram from another epoch
therefore cannot enter `target_received`/`echoed`. A missing epoch key means zero
datagrams for that epoch (the target was alive and its map was read), not
"unknown". If the target has no epoch map, the fields are null and
`target_epoch_isolated=false`.

Tests: `internal/target/epoch_test.go` (two epochs, headerless packet not
attributed to epoch 0, Reset clears); `internal/runner/udp_record_test.go`
(epoch map overrides global count; unavailable target stays null).

## Quiesce

`quiesceTarget` now returns a structured `QuiesceResult{Status, OK, TimedOut,
ElapsedMS, Datagrams, Reason}`. Status is `confirmed`, `timeout` or
`unavailable`; a timeout is recorded as such, never as success. It is called
after warmup for UDP (so the result is visible), but the measured target count no
longer depends on it: epoch isolation is the primary guarantee. `target_quiesce_status`
is stored in every UDP rep.

## Limit

The target attributes an epoch only when the datagram starts with the generator
header; a packet without it is counted globally and echoed, but not attributed.
The epoch map is in-process and not persisted across a target restart, which is
the same lifetime as a run.
