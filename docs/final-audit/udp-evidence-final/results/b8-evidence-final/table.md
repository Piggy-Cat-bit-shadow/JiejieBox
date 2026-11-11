# B8 table regenerated from raw JSON

Loss is shown only when the record says loss_estimable=true; `not_returned` is
writes-unique and is a deadline fact, not network-confirmed loss. Legacy runs
predate the split observation fields and are labelled `legacy_*`.

| scenario | rate pps | drain ms | writes | unique | target | not_returned | loss % | drain status |
|---|---:|---:|---:|---:|---:|---:|---:|---|
| masque-connect-ip | 50 | 8000 | 250 | 247 | 249 | 3 | 1.20 | complete_quiet |
| masque-connect-ip | 50 | 8000 | 250 | 246 | 248 | 4 | 1.60 | complete_quiet |
| masque-connect-ip | 50 | 8000 | 250 | 247 | 248 | 3 | 1.20 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1230 | 1237 | 20 | 1.60 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1223 | 1237 | 27 | 2.16 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1222 | 1233 | 28 | 2.24 | complete_quiet |
| masque-connect-ip | 500 | 8000 | 2500 | 1150 | 1168 | 1350 | 54.00 | complete_quiet |
| masque-connect-ip | 500 | 8000 | 2500 | 1334 | 1350 | 1166 | 46.64 | complete_quiet |
| masque-connect-ip | 500 | 8000 | 2500 | 1640 | 1857 | 860 | 34.40 | complete_quiet |
| trusttunnel-h2 | 50 | 8000 | 250 | 250 | 250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 50 | 8000 | 250 | 250 | 250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 50 | 8000 | 250 | 250 | 250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1248 | 1248 | 1248 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 500 | 12000 | 2500 | 2500 | 2500 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 500 | 12000 | 2499 | 2499 | 2499 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 500 | 12000 | 2498 | 2498 | 2498 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 500 | 8000 | 2500 | 2282 | 2331 | 218 |  | window_elapsed_tail_active |
| trusttunnel-h2 | 500 | 8000 | 2500 | 2500 | 2500 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 500 | 8000 | 2500 | 2488 | 2500 | 12 |  | window_elapsed_tail_active |
