# Cross-wrapper capability matrix (audit Q19)

The matrix lists the wrappers that actually exist and are relevant, and settles each capability with
a real-I/O test in this tree or an explicit dependency-owned cell. It replaces the earlier external
table with a durable in-repo one; it does not invent a chain that does not exist.

Legend: **P** proven by a real-I/O test in this tree; **D** dependency-owned (no in-repo observation
point; accepted as such, cell not claimed as proven); **N/A** the combination does not exist.

| capability | v2rayquic | MASQUE | HTTP tunnel + UoT | HY2 / TUIC | XHTTP XMUX |
|---|---|---|---|---|---|
| deadline | P `transport/v2rayquic/client_slow_stream_test.go` | P `transport/masque/backpressure_test.go` | P `transport/http/batch_through_timeout_test.go` | P `protocol/hysteria2/outbound_carrier_recovery_test.go`, `capability_surface_test.go` | P `transport/v2rayxhttp/deadline_test.go` |
| half-close | P `transport/v2rayquic/stream_error_test.go` | N/A (CONNECT-IP carries packets, not byte streams) | P `transport/http/client_h2_close_test.go` | P carrier recovery tests (`CloseWrite` on the stream path) | P `transport/v2rayxhttp/http2_test.go` |
| batch | P `transport/v2rayquic/client_slow_stream_test.go` (batch chunking) | P `transport/masque/batch_chunk_test.go` | P `transport/http/batch_*_test.go` | **D** pinned sing-quic stream exposes no `ReadBuffer`/`WriteBuffer`; `capability_surface_test.go` measures the absence | P `transport/v2rayxhttp/xmux_packetup_batch_test.go` |
| NAT | P `transport/v2rayquic/stream.go` ownership tests | P `protocol/masque/carrier_connectip_test.go` | P `transport/http/capsule_*_test.go` | P `protocol/tuic/udp_relay_mode_test.go` | N/A (request pool, not a flow NAT) |
| headroom | P `transport/v2rayquic/client_slow_stream_test.go` | P `transport/masque/batch_owned_test.go` | P `transport/http/batch_copy_packet_test.go` | **D** same missing buffer interface as batch | P `transport/v2rayxhttp/packetup_control_headroom_test.go` |
| partial-write | P `transport/v2rayquic/client_slow_stream_test.go` | P `transport/masque/backpressure_test.go` | P `transport/http/batch_copy_packet_test.go` | P `protocol/hysteria2/capability_surface_test.go` (256 KiB through the real conn) | P `transport/v2rayxhttp/packetup_drain_flow_test.go` |
| Addr | P `transport/v2rayquic/client_migration_test.go` | P `protocol/masque/carrier_connectip_test.go` | P `transport/http/client_h2_pool_reset_test.go` | P `protocol/hysteria2/outbound_carrier_recovery_test.go` | P `transport/v2rayxhttp/xmux_test.go` |
| exit witness | P pool/stream close counted in `transport/v2rayquic/residency_test.go` | P queue release in `transport/masque/backpressure_test.go` | P close/release in `transport/http/capsule_shutdown_test.go` | P session close in `protocol/hysteria2/outbound_carrier_recovery_test.go`, TUIC twin | P `transport/v2rayxhttp/xmux_inventory_test.go` (Released) |

The three **D** cells are the batch/headroom pair on HY2/TUIC. They are not asserted as proven: the
pinned stream type implements `io.Reader`/`io.Writer` and deadlines, not the fork's buffer interfaces,
which is measured rather than assumed in `protocol/hysteria2/capability_surface_test.go`. The matrix
therefore accepts dependency-side evidence for those two cells instead of building a test that cannot
observe them. If a future pin adds the buffer interfaces, that test fails its two negative assertions
and the cells must be re-run as proven.

N/A cells are stated with the reason in the cell; a missing combination is not counted as a pass.
