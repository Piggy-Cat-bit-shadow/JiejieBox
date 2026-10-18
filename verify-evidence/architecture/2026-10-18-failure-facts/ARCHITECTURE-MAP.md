# Architecture map — where this change sits

This is the event/resource map used for the construction and the adversarial
review. It describes the current tree, not a proposal.

## One flow

```text
inbound connection
  └─ route.Router.RouteConnection
       ├─ match rule / outbound
       ├─ resolveOutbound(commit=true)
       │    group -> Selected/SelectForFlow -> next edge -> leaf
       │    chain = [group, ..., leaf]
       ├─ metadata.OutboundChain / RouteOutbound / TrafficClass published
       └─ if a failover group owns the dial:
              routeDialer -> failoverDialer{leaf, group, metadata}
              -> group.DialWithFailover
                   -> resolve member per attempt (commit)
                   -> leaf.DialContext
                   -> on failure: RetryThisFlow / PenalizeMemberGlobally
                   -> alternate member (one flow budget)
          else:
              routeDialer = leaf
              -> ConnectionManager.NewConnection
```

- `route/route.go:resolveOutbound` is the one walk that turns a group into the
  leaf that will carry the flow; the packet path already passes that leaf.
- `route/failover.go:failoverDialer` is only a dial owner shim; it is not a
  physical hop and does not create a second resource pool.
- `route/conn.go` reads `ConnectionCopyTuner` from the object that actually
  dialled; it does not need a group capability.
- `route/reference.go` walks the outbound manager, so a leaf shared by several
  groups is visited once. Groups own no pool to trim.

## Resource ownership

| Fact/resource | Owner | This change |
| --- | --- | --- |
| Start/Close, cleanup registration | `adapter.Scope` | untouched |
| Network generation / rebind lease | `common/runtimecoord` | untouched |
| Pause/level vs background allow | `common/power` | untouched |
| Connection pool / idle release | the leaf outbound or transport | untouched; group not made an owner |
| Per-flow selection/commit | `protocol/group` + route walk | untouched; facts only classify an error |
| Failure fact | the protocol that observed the failure | new optional producer; Mieru wires its existing classifier |
| Retry/penalty policy | `protocol/group` | facts are read here; policy remains local |
| Dial/DNS/IPv6 | `common/dialer` / route | untouched |

## What was examined and rejected

1. **Capability forwarding through `failoverDialer`**: it hides eight optional
   interfaces, but the code and tests establish they are unreachable from the
   dialer argument today (packet path passes the leaf). Forwarding them would
   add no live behaviour; rejected as churn.
2. **Idle/reuse methods on groups**: leaves are already enumerated by the
   reference manager; adding group methods would create a second trim owner.
   Rejected.
3. **Buffer geometry / zero-copy wrapper change**: `route/conn.go` already
   resolves the leaf and unwraps the destination writer; `interrupt.Conn`
   forwards `Upstream`. No missing capability was proven, so no change.
4. **A universal failure enum**: rejected; the existing architecture's
   `EADDRNOTAVAIL` example proves one errno means different things to dialer,
   ledger and retry. The new fact is optional and attempt-scoped.
5. **Per-flow facts on a shared outbound**: rejected; facts live on the error,
   not on the leaf, so one flow cannot poison another.

## Why Mieru is the first producer

Mieru's fallback classifier already distinguishes:

- a valid SOCKS reply (target),
- a transport/handshake failure (path),
- caller cancellation,
- ambiguous TCP EOF (Unknown).

Exporting that existing proof is minimal and does not change its wire
behaviour. Other protocols do not have an equivalent proven stage yet; their
errno policy is left unchanged instead of guessing.
