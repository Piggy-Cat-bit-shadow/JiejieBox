# Fork audit, 2026-11-04

Three reports from one working session. They are records of what was checked at that time, not
statements about the current source; `BASELINE.md` and `modules/` remain the entry points.

| Report | Subject | Evidence level |
|---|---|---|
| `AUDIT-RESPONSE.md` | An independent audit found three P1 defects (Apple integration would drop the loopback work, a client pin referenced a member it did not define, AnyTLS re-entered admission with a permit held). Each fix, its proof, and the statements that had to be corrected or downgraded. | source + CI runs named in the file |
| `DEPENDENCY-FORKS.md` | The ten dependency forks measured with a local clone and both remotes rather than the GitHub compare API, which reports 0/0 for cross-fork comparisons here (sing-tun's real answer was 33 behind). Seven needed nothing; `sing-tun`, `cronet-go` and the Apple client were merged. | measured, ancestry only |
| `IOS-COMMAND-SOCKET.md` | The second device failure of the command socket path (`socket path is 110 bytes, the kernel allows 103`), why the first fix moved the problem instead of solving it, the budget-based resolver that replaced it, and the Darwin test that binds a deep path for real. | unit tests + a macOS CI run |

Two things worth carrying forward from these:

* **"Behind = 0" is an ancestry statement, not a behaviour statement.** It says the fork contains
  upstream's commits. None of the ten repositories was exercised on a device.
* **A path is not fixed by choosing another directory.** The first iOS fix moved the socket to the
  process temporary directory and assumed it is short; inside a Packet Tunnel it is the container's
  own `tmp`, 92 bytes before a name. The replacement sizes the *name* to the budget the directory
  leaves, and only a Darwin `bind` can prove the result.
