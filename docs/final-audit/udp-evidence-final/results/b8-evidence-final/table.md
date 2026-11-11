# B8 table regenerated from raw JSON

Loss is shown only when the record says loss_estimable=true; `not_returned` is
writes-unique and is a deadline fact, not network-confirmed loss. Legacy runs
predate the split observation fields and are labelled `legacy_*`.

| scenario | rate pps | drain ms | writes | unique | target | not_returned | loss % | drain status |
|---|---:|---:|---:|---:|---:|---:|---:|---|
| masque-connect-ip | 50 | 8000 | 250 | 245 | 248 | 5 | 2.00 | complete_quiet |
| masque-connect-ip | 50 | 8000 | 250 | 244 | 248 | 6 | 2.40 | complete_quiet |
| masque-connect-ip | 50 | 8000 | 250 | 242 | 246 | 8 | 3.20 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1230 | 1239 | 20 | 1.60 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1226 | 1240 | 24 | 1.92 | complete_quiet |
| masque-connect-ip | 250 | 8000 | 1250 | 1159 | 1171 | 91 | 7.28 | complete_quiet |
| masque-connect-ip | 500 | 8000 | 2500 | 1218 | 1232 | 1282 | 51.28 | complete_quiet |
| masque-connect-ip | 500 | 8000 | 2500 | 1584 | 1600 | 916 | 36.64 | complete_quiet |
| masque-connect-ip | 500 | 8000 | 2500 | 1371 | 1381 | 1129 | 45.16 | complete_quiet |
| trusttunnel-h2 | 50 | 8000 | 250 | 250 | 250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 50 | 8000 | 250 | 250 | 250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 50 | 8000 | 250 | 250 | 250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 250 | 8000 | 1250 | 1250 | 1250 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 500 | 12000 | 2500 | 2500 | 2500 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 500 | 12000 | 2500 | 2500 | 2500 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 500 | 12000 | 2500 | 2500 | 2500 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 500 | 8000 | 2494 | 2494 | 2494 | 0 | 0.00 | complete_quiet |
| trusttunnel-h2 | 500 | 8000 | 2500 | 2205 | 2500 | 295 |  | window_elapsed_tail_active |
| trusttunnel-h2 | 500 | 8000 | 2500 | 2500 | 2500 | 0 | 0.00 | complete_quiet |
