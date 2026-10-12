# Architecture boundaries worth preserving

- **ControlPath vs PhysicalPath:** groups/selectors decide a route; physical hops carry traffic. Start-time validation is read-only and consumer-first, and must not preview selection, dial, resolve DNS, or mutate group state.
- **Network epoch:** routing, DNS and reuse react to one monotonic transition identity. Coordinator publication is not itself a rebuild; stale resources retire lazily when demanded.
- **Platform boundary:** platform signals are facts delivered to core/libbox; Apple UI source is pinned independently as one source revision shared by iOS and macOS builds.
- **Release boundary:** product CI produces and proves artifacts; release downloads and re-verifies those exact bytes rather than rebuilding them.

See the module files for source anchors, provenance and acceptance limits.
