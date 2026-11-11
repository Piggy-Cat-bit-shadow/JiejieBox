# B8 table regenerated from raw JSON

Loss is shown only when the record says loss_estimable=true; `not_returned` is
writes-unique and is a deadline fact, not network-confirmed loss. Legacy runs
predate the split observation fields and are labelled `legacy_*`.

| scenario | rate pps | drain ms | writes | unique | target | not_returned | loss % | drain status |
|---|---:|---:|---:|---:|---:|---:|---:|---|
| masque-connect-ip | 100 | 2000.1 | 500 | 497 | 497 | 3 |  | legacy_complete |
| masque-connect-ip | 100 | 2000.3 | 500 | 487 | 494 | 13 |  | legacy_complete |
| masque-connect-ip | 100 | 2000.9 | 500 | 493 | 497 | 7 |  | legacy_complete |
| masque-connect-ip | 250 | 2000.3 | 1250 | 1185 | 1196 | 65 |  | legacy_complete |
| masque-connect-ip | 250 | 2000.3 | 1250 | 1224 | 1242 | 26 |  | legacy_incomplete |
| masque-connect-ip | 250 | 2000.5 | 1250 | 1140 | 1234 | 110 |  | legacy_incomplete |
| masque-connect-ip | 250 | 4000.3 | 1250 | 1227 | 1240 | 23 |  | legacy_complete |
| masque-connect-ip | 250 | 4000.8 | 1250 | 1213 | 1225 | 37 |  | legacy_complete |
| masque-connect-ip | 250 | 4000.9 | 1250 | 1229 | 1237 | 21 |  | legacy_complete |
| masque-connect-ip | 300 | 4000.2 | 1500 | 1119 | 1130 | 381 |  | legacy_complete |
| masque-connect-ip | 300 | 4000.4 | 1500 | 1373 | 1392 | 127 |  | legacy_complete |
| masque-connect-ip | 300 | 4000.5 | 1500 | 1471 | 1493 | 29 |  | legacy_complete |
| masque-connect-ip | 350 | 4000.0 | 1750 | 1319 | 1331 | 431 |  | legacy_complete |
| masque-connect-ip | 350 | 4000.6 | 1750 | 1483 | 1499 | 267 |  | legacy_complete |
| masque-connect-ip | 350 | 4000.6 | 1750 | 1566 | 1718 | 184 |  | legacy_complete |
| masque-connect-ip | 400 | 4000.2 | 2000 | 1244 | 1252 | 756 |  | legacy_complete |
| masque-connect-ip | 400 | 4000.4 | 2000 | 1301 | 1393 | 699 |  | legacy_complete |
| masque-connect-ip | 400 | 4000.7 | 2000 | 1485 | 1502 | 515 |  | legacy_complete |
| masque-connect-ip | 500 | 2000.1 | 2500 | 1682 | 1701 | 818 |  | legacy_complete |
| masque-connect-ip | 500 | 2000.1 | 2500 | 1205 | 1259 | 1295 |  | legacy_incomplete |
| masque-connect-ip | 500 | 2000.9 | 2500 | 1220 | 1231 | 1280 |  | legacy_complete |
| masque-connect-ip | 500 | 4000.4 | 2500 | 1322 | 1331 | 1178 |  | legacy_complete |
| masque-connect-ip | 500 | 4000.6 | 2500 | 1498 | 1510 | 1002 |  | legacy_complete |
| masque-connect-ip | 500 | 4000.6 | 2500 | 1405 | 1472 | 1095 |  | legacy_complete |
| trusttunnel-h2 | 100 | 2000.2 | 500 | 500 | 500 | 0 |  | legacy_complete |
| trusttunnel-h2 | 100 | 2000.3 | 500 | 500 | 500 | 0 |  | legacy_complete |
| trusttunnel-h2 | 100 | 2000.9 | 500 | 500 | 500 | 0 |  | legacy_complete |
| trusttunnel-h2 | 250 | 2000.1 | 1250 | 51 | 140 | 1199 |  | legacy_complete |
| trusttunnel-h2 | 250 | 2000.7 | 1250 | 1041 | 1250 | 209 |  | legacy_incomplete |
| trusttunnel-h2 | 250 | 2000.9 | 1250 | 1211 | 1250 | 39 |  | legacy_incomplete |
| trusttunnel-h2 | 250 | 4000.1 | 1250 | 1250 | 1250 | 0 |  | legacy_complete |
| trusttunnel-h2 | 250 | 4000.4 | 1250 | 1250 | 1250 | 0 |  | legacy_complete |
| trusttunnel-h2 | 250 | 4000.5 | 1250 | 1250 | 1250 | 0 |  | legacy_complete |
| trusttunnel-h2 | 500 | 2000.5 | 2500 | 1567 | 1621 | 933 |  | legacy_incomplete |
| trusttunnel-h2 | 500 | 2000.6 | 2500 | 1366 | 2353 | 1134 |  | legacy_incomplete |
| trusttunnel-h2 | 500 | 2000.7 | 2498 | 1014 | 1053 | 1484 |  | legacy_incomplete |
| trusttunnel-h2 | 500 | 4000.2 | 2500 | 1752 | 2299 | 748 |  | legacy_incomplete |
| trusttunnel-h2 | 500 | 4000.3 | 2499 | 1481 | 1677 | 1018 |  | legacy_incomplete |
| trusttunnel-h2 | 500 | 4000.8 | 2500 | 1427 | 1447 | 1073 |  | legacy_incomplete |
| trusttunnel-h2 | 500 | 8000.1 | 2500 | 2500 | 2500 | 0 |  | legacy_complete |
| trusttunnel-h2 | 500 | 8000.6 | 2500 | 2500 | 2500 | 0 |  | legacy_complete |
| trusttunnel-h2 | 500 | 8000.8 | 2500 | 2500 | 2500 | 0 |  | legacy_complete |
| trusttunnel-h2 | 500 | 8000.9 | 2500 | 570 | 1559 | 1930 |  | legacy_complete |
| trusttunnel-h2 | 500 | 8001.0 | 2499 | 2133 | 2254 | 366 |  | legacy_incomplete |
| trusttunnel-h2 | 500 | 8001.2 | 2500 | 2500 | 2500 | 0 |  | legacy_complete |
