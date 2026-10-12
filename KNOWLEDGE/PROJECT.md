# Project delta

This fork extends the upstream testing baseline with hardened lifecycle/network-epoch handling, start-time configuration/path validation, routing/DNS/sniff performance and safety work, server protocol work (notably MASQUE/Naive-related behavior), and product-specific Apple/Android/Windows release plumbing.

The initial upstream-derived changes visible in this history are not automatically fork inventions. For example MASQUE (`b83089ec`) and a Naive dependency update (`f4c0c412`) are upstream-inherited; subsequent fork fixes are documented by module. The fork range contains 1,834 commits from `e4b7e692..a267e99e`; module files deliberately index only commits that explain current behavior.

Current release identity is `v0.1.6`. Since `HEAD` equals that tag, its implementation baseline is also the release baseline.
