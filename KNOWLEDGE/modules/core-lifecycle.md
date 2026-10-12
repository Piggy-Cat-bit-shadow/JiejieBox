# Core, lifecycle, memory and validation

## Scope and provenance

This module covers `adapter.Scope`, runtime coordination, lifecycle/reuse, Apple device facts, and start-time physical-path validation. Fork commits: `8c7b230d` (FEATURE), `4494c962`/`5f56f02c`/`09964183`/`93385793` (MEMORY), `9dd91d51`/`482e4d3a` (HARDENING), and `01262510`/`31f250ff` (FEATURE/FIX). They are **OWN**; `7e13f461` absorbs the relevant upstream interface-getter reset.

## Current design

`common/runtimecoord` publishes generations, registrations and cancellable leases; it does not own protocol state, dialing, or closing. `box.go` installs resolver/ownership facts and route-derived delivery after router compilation. `route/reference.go` owns retirement inventory. `adapter.Scope` serializes ownership cleanup.

## Why / hard invariants

The design prevents stale network work, duplicated transport creation, false validation refusal, and false device power boundaries. Coordinator reactions happen outside coordinator/transition locks; a stale lease cannot release a newer one. A full `IdleConnectionKeeper` is deliberately not required where the real HTTP manager only supports `IdleConnectionReleaser`. See `INVARIANTS.md` for the non-negotiable rules.

## Verification and cautions

Evidence: `common/runtimecoord/*_test.go`, `box_lifecycle*_test.go`, `adapter/lifecycle*_test.go`, `route/reference_*_test.go`, `common/physicalpath/*_test.go`, `box_physicalpath*`, and `box_static_validation_*`.

Known limits: cancellation only reaches operations that observe their context; WireGuard reopen has bounded-latency behavior. `sing-mux` has no safe no-new-stream state and Cronet/Naive only offer destructive close, so they cannot be reuse-drained locally. DoH3 body-read timing and x/net/http2 unread-body close are dependency limitations. Real-device Apple behavior is **UNVERIFIED** beyond named reports.
