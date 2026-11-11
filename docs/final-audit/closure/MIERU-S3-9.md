# Mieru S3-9: idle-underlay recovery race

## Reproduction (current HEAD, SDK v3.39.0)

```
go test -race -count=20 -run TestMieruClientObservesServerClosingAnIdleUnderlay ./protocol/mieru
# 19 pass, 1 fail (5 %)
go test -count=20 -run TestMieruClientObservesServerClosingAnIdleUnderlay ./protocol/mieru
# 20/20 pass
```

Failure is at the **new dial after the idle close** (test line 140), not at the
blocked read the test was written for:

```
failed to read socks5 connection response from the server: EOF
```

## What it means

The test proxy (`startIdleClosingTCPProxy`) closes the TCP underlay after an idle
interval; the client's next `DialContext` can still pick that underlay in the
pick-to-dial window inside the SDK's `Mux.DialContext`/`maybePickExistingUnderlay`
and the SOCKS response read hits EOF. The test's expectation — a new dial must
build a new underlay — is the product behaviour it documents, so this is a
recovery race, not a flaky assertion.

Under `-race` the window widens (5 %); without race it did not reproduce in 20
runs, which is why earlier suites passed.

## Why no fix in this round

A correct fix needs either (a) a bounded, error-classified retry on a fresh
underlay when a reused one dies before the SOCKS reply, or (b) an SDK change to
re-check the underlay before `AddSession`. Both alter connection semantics
(possible double target connection, retry budget) and the pool is in the
un-forked SDK; the instruction's stopping rule applies. Status: **UNRESOLVED**,
with this reproduction and the upstream-patch direction. It does not block the
other closures.

## Resource side (S3-2)

The earlier in-process probe observation stands: 20 sequential dials add ≤16 FDs
and stay flat for 40 s idle; 200-dial bursts add diminishing amounts (55→73→83)
and do not reclaim within 10 s. In-process counts include the test inbound, so
this is still `DEFERRED`; it needs the paced-UDP/two-process harness to attribute
client FDs only.
