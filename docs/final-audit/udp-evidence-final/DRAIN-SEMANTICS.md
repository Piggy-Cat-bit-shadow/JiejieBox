# Drain semantics

## State model

`runUDP` no longer ends with a single `drainComplete` boolean. It records:

| field | meaning |
|---|---|
| `observation_ms` | configured post-send horizon (`-udp-drain`; 0 = loader default 500 ms when paced, `-1` disables) |
| `observation_elapsed_ms` | how long the horizon actually ran |
| `observation_complete` | the horizon ran to its end with a live context; it is true even when the tail is active |
| `quiet_period_ms` | time since the last reply at the end |
| `tail_active` | last reply landed inside the guard (`max(250 ms, 10 % of window)`) |
| `drain_status` | `complete_quiet`, `window_elapsed_tail_active`, `canceled`, `zero_window` |
| `loss_estimable` | `drain_status==complete_quiet` (window complete **and** tail quiet) **and** sequence tracking exact |
| `not_returned_by_deadline` | `writes - unique`, always recorded |
| `sequence_tracking_complete` | false once the sequence window is full |

`drainCompleteFor` was removed; `drainStateFor` is the pure function and is unit
tested for cancellation, zero window, tail-at-edge, quiet tail and no replies. A
full window with an active tail is `observation_complete=true, tail_active=true,
loss_estimable=false` — the three facts are independent.

## Loss semantics

- `lost` / `loss_percent` are emitted **only** when `loss_estimable=true`.
  They mean "no reply before the declared observation deadline", not
  network-confirmed loss; the report states that explicitly.
- `not_returned_by_deadline` and its percent are recorded even when the window is
  incomplete. A reader can see how much was missing without calling it loss.
- A full window with no replies at all is `complete_quiet` (the window ran; no
  tail was observed inside it), but the loss figure is still labelled deadline
  based.
- `DrainComplete` remains in JSON as an alias of `loss_estimable` for readers of
  v1.1; it is no longer the primary signal, and old JSON with `drain_complete=true`
  is not reinterpreted as proof of a complete tail.

## Guard policy

The guard is operational, not proof: 250 ms minimum, 10 % of the window
otherwise. It is configurable (`UDPQuietGuard`) and is recorded indirectly via
`quiet_period_ms`/`tail_active`. A run whose last reply is within the guard is
`window_elapsed_tail_active` and cannot emit loss, no matter how long the window
was.

## Tests

- `internal/loadgen/drain_test.go`: five state-machine cases; sequence-window
  truncation never counts an unattributable duplicate as unique.
- `internal/loadgen/udp_integration_test.go`: paced round trip, duplicate,
  reorder, delayed replies and context cancellation (the existing integration
  suite was kept; expectations now use the split fields).
- `internal/runner/udp_record_test.go`: estimable → loss; tail-active →
  null + deadline miss; sequence truncation → null loss; epoch target; target
  unavailable → null.
