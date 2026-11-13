# v0.1.47 鍙戝竷浜ゆ帴

## 鍥哄畾韬唤

| 椤?| 鍊?|
| --- | --- |
| 鐗堟湰 | `0.1.47` |
| tag | `v0.1.47` |
| tag 鍓ョ鎻愪氦 | `c50336676eb90e03792c81284c736c1a152ff684` |
| Release | https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.47 |
| Kernel release | [43519793788](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43519793788) |
| Apple 婧愮爜 pin | `aa8e16aa21a7f28aefc944fac76086c144195e3b` |
| Libbox | `jiejie-libbox-apple-0.1.47.tar.gz`锛?4,274,719 bytes |
| Libbox slices | `ios-arm64`, `macos-arm64` |
| Libbox build tags | `with_trusttunnel`, `with_awg` 鍧囧瓨鍦?|

Libbox manifest 鐨?`parent_sha` 涓?`c50336676eb90e03792c81284c736c1a152ff684`锛宍jj_version` 涓?`0.1.47`锛涙瀯寤烘爣绛惧寘鍚?`with_quic,with_wireguard,with_utls,with_naive_outbound,with_xhttp,with_trusttunnel,with_awg,...`銆?
## 鍏紑闄勪欢

鍏?8 椤癸細

| 闄勪欢 | SHA256 |
| --- | --- |
| `Jiejiebox-v0.1.47-windows-x64.exe` | `255ae2f6b9f1e856528d46b3e120dbd313c655f0803ce45cfade9d0014baac16` |
| `jiejie-ios-selfsign-unsigned-0.1.47.ipa` | `db3d17da807d88af1f1e88ad8140e4ae937adbfdb22633e4eac570aa63bb060b` |
| `jiejie-libbox-apple-0.1.47.tar.gz` | `c46425f742357fb3f522ca5b3ccc9b6dc6070644c1a65d893463779957e204f8` |
| `jiejie-sing-box-android-arm64-v0.1.47.tar.gz` | `29597bf6b68ae81a24e8afa4321e25db414779b3f566533e0cd776bbe5ac2b31` |
| `jiejie-sing-box-linux-amd64-v0.1.47.tar.gz` | `ff68689509278379e0a4d14730d9fc6c57b1ac68539b88d3acf2b24d95135b2a` |
| `jiejie-sing-box-macos-arm64-v0.1.47.tar.gz` | `a580d1fd63c37ba9ace52d867c1246c8f88440c5e8bc48312992326d1925792e` |
| `jiejie-sing-box-windows-amd64-v0.1.47.tar.gz` | `e02d9ffb1b5e3611f4932f2d173cc527d55a596babfcdb70620d0fdfc5779dd8` |
| `SHA256SUMS` | 璧勪骇鏍￠獙鍜屾竻鍗曪紝746 bytes |

`SHA256SUMS` 鏈韩鏈湪娓呭崟鍐呰嚜鏍￠獙锛涗笂琛ㄥ悇琛屾潵鑷闄勪欢銆?
## 楠岃瘉

`Kernel release 43519793788` 鐨勪互涓嬩綔涓氬叏閮ㄦ垚鍔燂細

- `freeze`
- `contract / release helper tests`
- `verify / verify`
- `verify / race checks`
- `linux / linux-amd64`
- `macos / macos-arm64`
- `windows / sing-box.exe (windows/amd64)`
- `desktop / unsigned-build / signed-test / install-lifecycle`
- `android / Android ARM64 core`
- `apple / resolve the Apple client source`
- `apple / libbox`
- `apple / ios`
- `apple / macos`
- `apple / ios-selfsign`
- `stage`
- `publish`

鍏紑椤甸潰鍥炶锛氶潪 draft銆侀潪 prerelease銆? 椤归檮浠躲€佹鏂囧凡鍐欏叆銆?
## 澶辫触涓庢仮澶嶈褰?
1. 棣栨 `Kernel release 43471548360` 鍦?`macos-latest` 鎺掗槦绾?15 鍒嗛挓鍚?`macos` 涓?`apple / libbox` 琚彇娑堬紱鏃?runner 鍒嗛厤锛屾湭鍒涘缓 tag 鎴?Release銆?2. 绗簩娆?`43475873935` 鍚屾牱鍦?15 鍒嗛挓鍚庤鍙栨秷銆?3. 绗笁娆?`43515885484` 鎴愬姛鎷垮埌 macOS runner锛屼絾 macOS 鍗曟祴澶辫触锛歚TestHalfCloseRefusedConfigurationIsSkipped` 鍦?`newHalfCloseFixture` 鍚姩 copy goroutine 鍚庢墠璁剧疆 `configErr`锛屾祴璇曞垵濮嬪寲涓庤寮傛璺緞绔炴€併€?4. 淇 `c503366`锛氭柊澧?`newHalfCloseFixtureWithConfigErr`锛屽湪 `manager.NewConnection` 涔嬪墠璁剧疆閰嶇疆閿欒锛屽苟鏂█ `Pending == 0`銆傞殢鍚?`43519793788` 鍏ㄧ豢骞跺彂甯冦€?
## 鏈畬鎴?
- TestFlight 涓婁紶銆佺鍚嶃€佺湡鏈洪獙鏀讹細鏈墽琛岋紝鎸?[TF 鏋勫缓鎸囧紩](../TESTFLIGHT-MAC.md) 鐙珛杩涜銆?- Satelite Android 瀹㈡埛绔?tag/APK 鍙戝竷锛氭湭鎵ц锛岄渶鍦?Satelite 浠撳簱瀵归綈 `versionName=0.1.47`銆乣coreCommit=c50336676eb90e03792c81284c736c1a152ff684` 骞跺畬鎴愮鍚嶉獙鏀躲€?