# NaiveProxy 鎬ц兘涓庢灦鏋勬繁搴﹁瀺鍚堬細绗竴杞柦宸ユ姤鍛?
- **鏃ユ湡**锛?026-10-20
- **绫诲瀷**锛氬疄闄呮柦宸?+ 鍥炲綊 + 鎻愪氦锛堥潪瀹¤锛?- **杈撳叆**锛歚01_NaiveProxy_鎬ц兘棰嗗厛涓庢灦鏋勬繁搴﹁瀺鍚坃瀹屾暣鏂藉伐鎻愮ず璇峗testing.md`銆乣NAIVEPROXY_DEEP_AUDIT_2026-10-20.md`
- **鏈疆瀹氭€?*锛?*鏂藉伐涓€杞紝宸叉彁浜ゅ苟鍙帹閫?*锛汸3锛圚3鈫扝2 鍥為€€锛夌粡鏍稿渚濊禆鐜扮姸鍚?*鎸夋垚鏈?鏀剁泭鏄惧紡鎺ㄨ繜鍒颁笅涓€杞?*锛岀悊鐢变笌绮剧‘璁捐瑙?搂6锛堜笉鏄瓑浜烘壒鍑嗭紝鏄竴涓湁璇佹嵁鐨勮寖鍥村垽鏂級銆?
---

## 1. 鐜板満銆丼HA 涓庡綊灞?
| 椤?| 鍊?|
|---|---|
| 涓讳粨 | `Piggy-Cat-bit-shadow/sing-box`锛屽垎鏀?`testing` |
| 鏈疆璧风偣 SHA | `11c4c1b27ff87a6ee686c9c1b578a333bbb726d4`锛堟湰鍦?= `origin/testing`锛?|
| 鏈疆缁撴潫 SHA | `84760a372`锛? 涓彁浜ゅ彔鍔犱簬璧风偣锛涘凡 fast-forward 鎺ㄩ€侊紝鎺ㄩ€佸悗 `HEAD...origin/testing` = `0 0`锛?|
| 瀹¤鎶ュ憡 SHA锛堝巻鍙诧級 | 璧风偣 `6edc18c60` 鈫?`7e42ca38e`锛涙湰杞殑璧风偣宸插惈骞惰绐楀彛 4 涓彁浜?|
| 骞惰绐楀彛褰掑睘 | `clients/apple`銆乣clients/desktop` 鐨?submodule 鎸囬拡淇敼**涓嶆槸鏈疆鐨?*锛屾湭 stage銆佹湭鎻愪氦 |
| cronet-go pin | 鏈敼鍔細`Piggy-Cat-bit-shadow/cronet-go@8c68ce89873c`锛坮oot + `all` + 29 涓?`lib/*`锛?|
| sing-tun pin | 鐢卞苟琛岀獥鍙ｅ湪瀹¤鏈浠?`ff4fbed67c0d` 鍗囧埌 `276576ea222c`锛屾湰杞捣鐐瑰凡鍖呭惈 |
| Native 杩愯鏃剁増鏈?| Cronet `Engine().Version()` = `154.0.8037.49`锛堟湰鏈哄疄娴嬶紝Windows/amd64锛?|
| 宸ュ叿閾?| `go1.25.5 windows/amd64`锛宍CGO_ENABLED=0`锛宍-tags with_purego`锛圵indows 瀹㈡埛绔?profile锛?|
| 鏈嶅姟绔?profile | `release/DEFAULT_BUILD_TAGS_OTHERS` |
| 娴嬭瘯鏈?| Windows 11 x64锛?6 閫昏緫鏍革紝loopback |

**骞惰绐楀彛闃叉姢**锛氭湰杞瘡娆″啓鍏ュ墠姣斿 `git status` / `git rev-parse HEAD`锛涗笁娆℃彁浜ら兘鍙?stage 鑷繁鍒涘缓鎴栦慨鏀圭殑鏂囦欢锛涙帹閫佸墠鍐嶆 `git fetch` 姣斿杩滅锛堣 搂8锛夈€?
---

## 2. 缁撹鎽樿

| 椤圭洰 | 鐘舵€?| 涓€鍙ヨ瘽 |
|---|---|---|
| **P1 Naive 脳 FailureFacts** | **DONE** | 鍑虹珯鐜板湪浼?*瀹屾垚 CONNECT 鎻℃墜**骞舵妸缁撴灉鏄犲皠鎴?`adapter.FailureFacts`锛涢『甯︿慨澶嶄簡涓€涓瘮瀹¤鎻忚堪鏇翠弗閲嶇殑鐪熷疄缂洪櫡锛?*407 浠庢潵涓嶆槸閿欒** |
| **P9 padding 鍏ㄩ浂鍥炲綊** | **DONE** | 256 涓?padding 鎶芥牱 脳 涓夋潯鐢熶骇鍐欒矾寰勶紱鍙樺紓楠岃瘉锛坄WriteZeroN`鈫抈Extend`锛夌‘璁ゆ祴璇曠湡鐨勪細绾?|
| **P7 鏂囨。榛樿鍊?* | **DONE** | `quic_congestion_control` 鐨勯粯璁ゅ€间粠"BBR"鏀逛负浜嬪疄锛圕UBIC锛宭ibrary default锛?|
| **P2 Cronet DoH** | **BLOCKED_BY_NATIVE_API** | 褰撳墠 pin 鏃犱换浣?Go 鍙厤缃殑 Secure DNS 寮€鍏筹紱`SetHostResolverRules` 鍙兘瑙ｅ喅鍩熷悕 MAP锛屼笉鑳芥秷闄?stray 杩炴帴銆備粖鏃ュ疄娴嬭杩炴帴浠嶅湪锛埪?锛?|
| **P3 H3鈫扝2 鍥為€€** | **DEFERRED锛堜笅涓€杞級** | 鍏抽敭鑳藉姏锛坧er-CONNECT 閫夋嫨锛夊凡璇侊紝浣嗛渶瑕?*鍏堟敼 cronet-go 鐨?`DialEarly`**锛涗緷璧栦粨 `main` 宸查鍏?pin 涓€涓彁浜わ紝椤诲紑鍒嗘敮鏈€灏忔敼鍔?+ 閲嶆柊 pin + 鐙珛瀵规姉鐭╅樀銆傝璁′笌鎻掑叆鐐瑰凡鍐欏ソ锛埪?锛?|
| **P8 Engine/绐楀彛榛樿** | **KEEP** | 鏃犳柊鐨勬椿璺冭礋杞芥暟鎹紱榛樿涓嶅姩 |

**鏈疆鏈€閲嶈鐨勬柊鍙戠幇**锛堝璁℃病鍐欍€佹柦宸ヤ腑瀹炴祴鍑烘潵鐨勶級锛歚Outbound.DialContext` 涔嬪墠璋冪敤 `DialEarly` 鍚?*浠庝笉璇诲彇 CONNECT 鍝嶅簲**锛屾墍浠ラ敊璇瘑鐮侊紙407锛夊湪 sing-box 閲?*涓嶆槸閿欒**鈥斺€旈毀閬?寤虹珛鎴愬姛"锛岀涓€娆?Read 寰楀埌 EOF銆備慨瀹屼箣鍚庡悓涓€涓祴璇曚粠"绌洪毀閬?鍙樻垚 `naive CONNECT handshake: unexpected response status: 407`锛埪?.2锛夈€?
---

## 3. P1锛歂aive 脳 鍏叡鏁呴殰浜嬪疄

### 3.1 瀹¤娌¤閫忕殑寮傛鐪熺浉

```
Outbound.DialContext            -- 鏃т唬鐮?return client.DialEarly(...)
  -> NaiveClient.DialEarly      -- 鍙?CreateConn + Start(CONNECT)锛屼笉璇诲搷搴?  -> BidirectionalConn.Start    -- 杩斿洖 nil 鏃舵祦宸叉彁浜わ紝CONNECT 200/407 灏氭湭鍒拌揪
  -> route/conn.go              -- ReportConnHandshakeSuccess 鍙€氱煡 inbound 渚э紝涓嶉┍鍔?Naive 鎻℃墜
     kickWriteHandshake        -- 妫€鏌?EarlyWriter/EarlyConn 鎺ュ彛锛汵aiveConn 涓嶅疄鐜帮紝浜庢槸涓嶅己鍒舵彙鎵?  -> copy loop                 -- 绗竴娆?Read 鎵?waitForHeaders
```

**缁撹**锛氭棫浠ｇ爜閲?`NaiveConn.HandshakeContext` **娌℃湁浠讳綍璋冪敤鑰?*銆傚洜姝わ細
- `DialEarly` 鐨?error 姘歌繙涓嶅彲鑳芥惡甯﹀崗璁簨瀹烇紙瀹冪湅涓嶅埌鍗忚锛夛紱
- 407 涓嶄細琚瘑鍒紝瀹㈡埛绔湅鍒扮殑鏄?杩炴帴鍚庣珛鍒?EOF"锛?- 鍦?`Outbound.DialContext` 澶栭潰鍖呬竴灞?`WrapFailure(err)` 灞炰簬瀹¤閲岃鐨?鍙慨鍒拌〃鐨?锛岀敋鑷虫洿绯熲€斺€斿畠浼氭妸 dial 闃舵鐨勯敊璇鏍囨垚鍗忚浜嬪疄銆?
### 3.2 瀹為檯鏀瑰姩涓庤瘉鎹?
鏂板 `protocol/naive/failure.go`锛?
- `dialNaiveTunnel(ctx, client, destination)`锛歚DialEarly` 鈫?**`HandshakeContext`** 鈫?鎴愬姛鎵嶈繑鍥烇紱澶辫触鍏?`Close` 鍐嶅垎绫汇€?- `naiveHandshakeFailureFacts(ctx, err)`锛氱獎鏄犲皠锛堜笅琛級銆?- `naiveDialerClient` 绐勬帴鍙ｏ細璁╁垎绫婚€昏緫鍙互**涓嶄緷璧?Native 搴?*鍋氬崟鍏冩祴璇曪紱鐢熶骇璺緞闆跺寘瑁咃紙`*cronet.NaiveClient` 鐩存帴婊¤冻锛夈€?- `naiveResponseStatus`锛氫粠渚濊禆鐨勯敊璇枃鏈噷鍙栫姸鎬佺爜锛堝悗缂€鍖归厤锛屽灞?`E.Cause` 涓嶅奖鍝嶏紱闈炴暟瀛?瓒婄晫杩斿洖"涓嶆槸鐘舵€?锛夈€?
`protocol/naive/outbound.go`锛?- TCP 璺緞涓?UoT 鐨?`naiveDialer.DialContext` 閮芥敼璧?`dialNaiveTunnel`銆俇oT 浼氳瘽楠戝湪涓€涓?CONNECT 閲岋紝蹇呴』鍦ㄥ啓 session header **涔嬪墠**鐭ラ亾闅ч亾琚嫆銆?
**鏄犲皠琛紙鏁呮剰绐勶級**

| 鎻℃墜缁撴灉 | facts | 鐞嗙敱 |
|---|---|---|
| CONNECT 407 | `{Handshake, Proxy}` | 浠ｇ悊鎷掔粷浜嗗嚟鎹紱缁濅笉鑳借褰撴垚鐩爣鎴栬矾寰勯棶棰?|
| CONNECT 鍏跺畠鐘舵€?| 鏃?fact | 鏈変笢瑗垮簲绛斾簡锛?03/502 鏃犳硶鍖哄垎鐩爣鎷掔粷涓庝唬鐞嗙瓥鐣?鈫?unknown 浼樹簬鐚?|
| `NetErrorConnectionRefused` / `NetErrorAddressUnreachable` | `{Handshake, Path}` | 鍐呮牳绾х殑"绔偣涓嶅彲杈?锛屼笌 group 宸蹭俊浠荤殑 ECONNREFUSED/EHOSTUNREACH 鍚岀被 |
| `NetErrorNameNotResolved` | `{Handshake, Local}` | DNS 绛栫暐/閰嶇疆锛屽睘浜庢湰杩涚▼ |
| TLS/璇佷功绫?| `{Handshake, Proxy}` | 绔偣璺戝埌浜?TLS 骞跺け璐?|
| **QUIC protocol/handshake 澶辫触** | **鏃?fact锛堝埢鎰忥級** | QUIC 澶辫触鍙瘉鏄?UDP 璺笉閫氾紝**涓嶈瘉鏄庤妭鐐瑰潖浜?*锛涙爣 Path 浼氭妸涓€涓?H2 鍋ュ悍鐨勮妭鐐瑰叏灞€闄嶆潈銆傝繖涓€鏉℃鏄洖閫€鐗规€у瓨鍦ㄧ殑鐞嗙敱 |
| timeout / reset / EOF / cancel / closed | 鏃?fact | group 鏈潵灏卞垽涓€э紱瓒呮椂涓嶆槸"璺緞姘镐箙娑堝け"鐨勮瘉鏄?|

娑堣垂鑰呭榻愶紙鏃犻渶鏀?group锛夛細`RetryThisFlow` 瀵?`Proxy/Handshake` 涓?`Path/Handshake` 閮芥槸 RETRY锛沗PenalizeMemberGlobally` 鍙 Path 涓旈潪瓒呮椂闄嶆潈鈥斺€斿凡鏈夋祴璇?`protocol/group/loadbalance_failure_facts_test.go` 瑕嗙洊杩欎袱绉嶅舰鐘躲€?
### 3.3 绔埌绔瘉鎹紙鐪熷疄 Native + 鐪熷疄 sing-box 鍑虹珯锛?
閰嶇疆锛歚mixed` inbound `127.0.0.1:18081` 鈫?`naive` outbound 鈫?鏈湴 Naive 鏈嶅姟绔紙鑷 CA锛宍trusted` 閫氳繃 `tls.certificate_path`锛夈€?
**姝ｇ‘瀵嗙爜**锛歚curl -x http://127.0.0.1:18081 http://127.0.0.1:18080/` 鈫?**200**銆?
**閿欒瀵嗙爜**锛堟湇鍔＄鐢ㄦ埛瀵嗙爜涓嶅悓锛夛細

```
outbound/naive[n]: open TCP connection to 127.0.0.1:18447
outbound/naive[n]: response received, protocol: h2, status: 407
outbound/naive[n]: handshake failed: unexpected response status: 407
connection: open connection to 127.0.0.1:18080 using outbound/naive[n]:
  naive CONNECT handshake: unexpected response status: 407
```

`curl` 寰楀埌 **502**锛堜唬鐞嗗け璐ワ級鑰屼笉鏄棫琛屼负鐨勭┖闅ч亾銆傛棩蹇楅噷绗竴娆″嚭鐜?`status: 407` 涔熸槸鏂颁唬鐮佺殑鍓骇鍝侊細浠ュ墠鎻℃墜鍑芥暟娌¤璋冪敤锛岃繖琛?debug 閮戒笉浼氬嚭鐜般€?
### 3.4 浠ｄ环锛堜竴杞?RTT锛屾湁杈圭晫锛?
- 姣忎釜**鏂?*闅ч亾鍦ㄧ涓€娆″啓涔嬪墠澶氱瓑涓€涓?CONNECT 鍝嶅簲銆傚弬鑰冩湇鍔＄锛圕addy forwardproxy锛夊湪鎷ㄧ洰鏍?*涔嬪墠**灏卞彂 200锛屾墍浠ヨ繖涓€涓槸闅ч亾寰€杩旓紝涓嶆槸鐩爣寰€杩斻€?- 娌℃湁鏀圭紦瀛橀鍖?`kickWriteHandshake` 鐨勬棦鏈夎涔夛紱娌℃湁涓烘湭鍚敤浠讳綍閫夐」鐨勭敤鎴峰鍔犵浜屾潯璺緞銆?- loopback 瀹炴祴锛氭彙鎵嬪寘鍚湪鏃㈡湁 5鈥? ms 鐨?connect 閲岋紙瀹¤鎺㈤拡娴嬬殑鏄悓涓€鏉?`DialContext` 璇箟锛夈€?*WAN 鐨?+1 RTT 鏈祴**锛坄NOT_RUN`锛夛紝杩欐槸鏈疆鍞竴娌℃湁鐪熷疄缃戠粶鏁版嵁鐨勫彇鑸嶏紝蹇呴』鐣欑粰涓撻」 Debug 鐢?120 ms 閾捐矾閲忓寲銆?- 鍥炴粴锛歚git revert` 璇ユ彁浜ゅ嵆鍙紱榛樿閰嶇疆銆佸崗璁瓧鑺傘€佽璇併€丄CL銆佺敓鍛藉懆鏈熷潎鏈敼鍙樸€?
### 3.5 浠嶆湭瑕嗙洊 / 宸茬煡杈圭晫

- **闈?407 鐨勭姸鎬佺爜**娌℃湁 facts锛堜繚瀹堬級銆傚鏋滃皢鏉ヨ瘉鏄庢煇绫荤姸鎬佺爜绋冲畾琛ㄧず鐩爣鎷掔粷锛屽啀鍗曠嫭鍔犮€?- **QUIC 澶辫触鏃?facts**锛氱瓑鍥為€€鐗规€ц惤鍦板悗锛屽け璐ヤ細鍦ㄥ洖閫€鍐呴儴琚秷鍖栵紝涓嶉渶瑕佸澶栬В閲娿€?- **鏁版嵁闃舵**锛堜氦鍑鸿繛鎺ュ悗鐨?Read/Write锛変笉浜х敓 facts锛岀鍚?`FailureStageData` 鐨?涓嶆槸涓€娆℃柊鎷ㄥ彿"璇箟銆?- `naiveResponseStatus` 渚濊禆 cronet-go 鐨勯敊璇枃鏈紙`naive_conn.go` 鐨?`E.New("unexpected response status: ", ...)`锛夈€傝嫢渚濊禆鏀规枃妗堬紝琛屼负閫€鍖栦负"鏃?fact"锛屼笉浼氫骇鐢熼敊璇?fact锛涜繖涓€鏉″凡鍐欒繘浠ｇ爜娉ㄩ噴锛屼笅涓€杞簲鍔犱竴鏉¤法浠?pin 鏂█銆?
---

## 4. P9锛歱adding 鍏ㄩ浂鍥炲綊锛堝惈鍙樺紓楠岃瘉锛?
鏂板 `protocol/naive/padding_zero_fill_test.go`锛岃鐩?0..255 鍏ㄩ儴 256 涓?padding 鎶芥牱锛?
| 璺緞 | 鏂█ |
|---|---|
| `writeFrame`锛堝垎閰嶅瀷锛宍writeChunked` 浣跨敤锛?| 甯ч暱 = 3+data+pad锛宲ayload 涓嶅彉锛?*padding 鍖哄叏闆?* |
| `writeBufferWithPadding`锛堝師鍦板瀷锛岃皟鐢ㄦ柟姹犲寲 buffer锛?| 鍚屼笂锛屼笖 free 鍖哄厛琚～ `0xAA`锛涘抚鍚庢墍鏈夊瓧鑺?*鏈瑕嗗啓**锛沗Start()==0` |
| `writeChunked`锛? MiB锛岃蛋婊?8 甯х獥鍙ｏ級 | 姣忓抚 padding 鍏ㄩ浂锛涚獥鍙ｅ悗鍓╀綑涓?raw锛宲ayload 鎬婚噺瀹堟亽 |
| padding=0 | 閫€鍖栫敤渚嬪浐瀹氫负鏄惧紡鏂█锛坄{0,3,0,'a','b','c'}`锛?|

**鍙樺紓楠岃瘉锛堢湡瀹炴墽琛岃繃锛屼笉鏄０绉帮級**锛氭妸 `inbound_conn.go` 鐨勪袱澶?`WriteZeroN(paddingSize)` 鎹㈡垚 `Extend(paddingSize)`锛?
```
TestAuditPaddingBytesAreAlwaysZeroInPlace  FAIL
  padding byte 0 (of 1) is 0xaa; the Naive layout requires zeros
TestAuditPaddingBytesAreAlwaysZeroChunked  FAIL
  padding byte 0 (of 1) is 0x77
```

鎭㈠鍚庡叏缁裤€?*杩欎笉鏄慨浜嗕竴涓綋鍓嶅瓨鍦ㄧ殑娉勬紡**锛岃€屾槸璁?褰撳勾閫犳垚 `cronet-go#6` / `sing-box#4002` 鐨勯偅娆℃敼鍔?鏃犳硶鍐嶆倓鎮勯€氳繃銆?
---

## 5. P2锛欳ronet DoH bootstrap锛圔LOCKED_BY_NATIVE_API锛屾湁褰撴棩瀹炴祴锛?
### 5.1 浠婃棩璇佹嵁锛堟湰杞噸鏂拌窇鍑烘潵鐨勶紝涓嶆槸寮曠敤瀹¤锛?
鐢ㄧ敓浜?`sing-box-client.exe`锛圵indows profile锛夎繛鏈湴 Naive 鏈嶅姟绔椂锛宻tderr 浠嶇劧鍑虹幇锛?
```
DEBUG outbound/naive[n]: open UDP connection to [2001:4860:4860::8888]:443
ERROR outbound/naive[n]: open UDP connection to [2001:4860:4860::8888]:443:
  local network has no public ipv6 path: ...
```

鍗筹細`quic:false`锛堥粯璁わ級銆乣SetEnableQuic(false)`銆乣dns_server_override=127.0.0.1:53`銆乣UseDnsHttpsSvcb=false` 鍏ㄩ儴鐢熸晥鐨勬儏鍐典笅锛?*寮曟搸鍚姩浠嶄細灏濊瘯 DoH/DoQ 鍒?Google Public DNS**锛屼笖璇?socket 璇锋眰缁?cronet dialer 妗ヨ繘鍏?sing-box dialer锛堟墍浠ュ彈璺敱/detour 绾︽潫锛夛紝浣?*涓嶇粡杩?`dns_router`**銆?
### 5.2 鎴戞牳瀵逛簡浠€涔?
| 妫€鏌?| 缁撴灉 |
|---|---|
| cronet-go 鏄惁鏆撮湶 Secure DNS/DoH 寮€鍏?| **娌℃湁**銆俙engine_params_experimental_options.go` 鍙湁 `SetAsyncDNS`銆乣SetDNSServerOverride`銆乣SetHostResolverRules`銆乣SetUseDnsHttpsSvcb` |
| `SetHostResolverRules` 鑳藉惁娑堥櫎璇ヨ繛鎺?| **涓嶈兘**銆侻AP 鍙奖鍝嶆寚瀹?host 鐨勮В鏋愶紱鏈満鍦?`ServerName` 鏄?IP 瀛楅潰閲忋€佹牴鏈病鍙戠敓鍩熷悕瑙ｆ瀽鏃讹紝杩炴帴鐓ф牱鍑虹幇 |
| 鏄惁瀛樺湪鍙鐢?DoH 鐨?Chromium experimental option锛堝彲浠?Go 渚т紶 JSON锛?| 褰撳墠 pin 鏈毚闇诧紱native 搴撴槸棰勭紪璇戜骇鐗╋紝鏂板閫夐」闇€瑕佹敼 C++ 鈫?灞炰簬 `BLOCKED_BY_NATIVE_API` |
| 鏄惁搴旇鐢辨垜浠粯璁ょ‖缂栫爜鍏叡 DNS / 鎶婄敤鎴?`server` 鍙樻垚闈欐€?IP | **涓嶅簲璇?*銆傛彁绀鸿瘝鏄庣‘绂佹锛屼篃浼氱牬鍧?detour/ECH/鍙屾爤绛栫暐 |

### 5.3 缁撹涓庝笅涓€姝?
- **涓嶅仛**"鐪嬭捣鏉ヤ慨濂戒簡"鐨?MAP 琛ヤ竵锛氬畠娌讳笉浜嗚繖鏉¤繛鎺ワ紝澹扮О淇ソ浼氳瀵笺€?- 涓嬩竴姝ワ紙鐙珛璇鹃锛夛細瑕佷箞涓婃父鍦?cronet-go 鏆撮湶 `secure_dns`/DoH 閰嶇疆锛堥渶瑕?Native 鏀瑰姩锛屾垚鏈珮锛夛紝瑕佷箞鎺ュ彈杩欐槸宸茬煡杈圭晫骞跺湪鏂囨。閲屽啓鏄庯紱濡傛灉鍙槸涓轰簡娑堟帀涓€鏉¤繛鎺ヨ€岃嚜缁存姢 Chromium patch锛屾寜鎻愮ず璇嶇殑鎴愭湰鍒ゆ柇搴旀斁寮冦€?- 鐜版湁杈冨皬鏀瑰杽鐨勫€欓€夛細鎶?`host_resolver_rules` 浣滀负**鐢ㄦ埛鍙厤**閫夐」鏆撮湶锛屽彧瑙ｅ喅"浠ｇ悊鍩熷悕蹇呴』璧?sing-box 瑙ｆ瀽"鐨勫満鏅紝骞舵槑纭爣娉?stray 浠嶅湪"銆傛湰杞?*鏈仛**锛氬畠娌℃湁瑙ｅ喅 P2 鐨勬牳蹇冮棶棰橈紝涓斾細鏂板涓€涓敤鎴峰彲瑙侀厤缃潰銆?
---

## 6. P3锛欻3鈫扝2 鍥為€€锛堜笅涓€杞紝璁捐涓庨樆濉炲凡钀藉湴锛?
### 6.1 涓轰粈涔堟病鍦ㄦ湰杞柦宸ワ紙绮剧‘鐨勯樆濉烇紝涓嶆槸"娌℃椂闂?锛?
per-CONNECT 閫夋嫨鑳藉姏鍦?Chromium 渚у凡璇侊紙`-force-quic` header锛夛紝浣?*sing-box 渚ф病鏈夊叆鍙?*锛?
- `cronet.NaiveClient.DialEarly(ctx, dest)` 鐨?header 鐢?`c.quicEnabled` 鍐冲畾锛坄naive_client.go:580-582` 鏃犳潯浠惰拷鍔?`-force-quic`锛夛紱
- `-force-quic` 鏄繚鐣欏ご锛宍ExtraHeaders` 浼氳鎷掔粷/璺宠繃锛宻ing-box 鏃犳硶浠庡閮ㄥ叧鎺夊畠锛?- 鍥犳"鍏?H3銆佸け璐ュ啀 H2"**蹇呴』**鍏堝湪 cronet-go 澧炲姞 per-dial 鍏ュ彛銆?
鎴戞牳瀵逛簡渚濊禆浠擄細`Piggy-Cat-bit-shadow/cronet-go` 鐨?`main` 宸插埌 `049f290`锛?Fix Windows UDP socket close callback registration"锛夛紝**棰嗗厛鎴戜滑 pin 鐨?`8c68ce8`**銆傛纭仛娉曟槸**浠?pin 寮€鍒嗘敮**鍋氭渶灏忔敼鍔紙涓嶆槸鎺ㄥ埌 main 椤哄甫鍚告敹鏈瘎瀹＄殑鎻愪氦锛夛紝鐒跺悗閲嶆柊 pin root 妯″潡锛坄all`/29 涓?`lib/*` 涓嶉渶瑕佸姩锛屽洜涓哄彧鏀逛簡 Go 渚э級銆?
### 6.2 涓嬩竴杞殑绮剧‘璁捐锛堝彲鐩存帴鎵ц锛?
**cronet-go 渚э紙绾?30鈥?0 琛岋級**

```go
// 鏃?API 琛屼负涓嶅彉
func (c *NaiveClient) DialEarly(ctx context.Context, destination M.Socksaddr) (NaiveConn, error) {
    return c.dialEarly(ctx, destination, c.quicEnabled)
}

// 鏂扮獎鍏ュ彛锛歱er-dial 閫夋嫨锛屼笉鏀?Engine 鍙傛暟
func (c *NaiveClient) DialEarlyWithQUIC(ctx context.Context, destination M.Socksaddr, quic bool) (NaiveConn, error) {
    return c.dialEarly(ctx, destination, quic)
}
```
鎶?`-force-quic` 鐨勮拷鍔犱粠 `c.quicEnabled` 鏀逛负鍙傛暟锛沗buildDialHeaders(dest, quic)` 鎶芥垚绾嚱鏁板苟鍔犲崟娴嬶紙鏃犻渶 Native 搴擄級銆?
**sing-box 渚э紙绾?120鈥?80 琛?+ 娴嬭瘯锛?*

鎻掑叆鐐瑰氨鏄湰杞柊澧炵殑 `protocol/naive/failure.go::dialNaiveTunnel`锛?
```text
if !fallbackEnabled || !h.quic {
    return dialNaiveTunnel(ctx, h.client, dest)          // 鏃ц涓猴紝榛樿璺緞
}
attempt := verdict.begin()
if verdict.available() {
    conn, err := dialNaiveTunnelWithQUIC(ctx, h.client, dest, true)   // 鍚?HandshakeContext
    if err == nil { verdict.clear(attempt); return conn }
    if ctx.Err() != nil || !isH3TransportFailure(err) { return nil, err }
    verdict.markBroken(attempt)
}
return dialNaiveTunnelWithQUIC(ctx, h.client, dest, false)
```

**鍒ゅ畾鏍?*锛坄protocol/naive/h3_fallback.go`锛岀害 60 琛岋級锛氬鐢?`transport/http/client.go` 鐨?*绠楁硶**锛堜笉鏄唬鐮侊紝涔熶笉鏄涓変釜鍏ㄥ眬 verdict锛夛細

- `begin()` 鍦ㄥ彂璧峰墠鍙栧簭鍙凤紱
- `claim(attempt)` 鍙帴鍙楁洿鏂扮殑搴忓彿锛圕AS锛夛紝淇濊瘉鏃?attempt 鐨勮繜鍒板け璐ヤ笉瑕嗙洊鏂版垚鍔燂紱
- `markBroken(attempt)`锛歝laim + 璁剧疆鍥哄畾绐楀彛锛堝缓璁?30 s锛屽彲閫夐厤缃級锛岀獥鍙ｅ唴涓嶅啀璇?H3锛?- `clear(attempt)`锛歝laim + 娓呴浂锛?- `InterfaceUpdated` 閲屼笌 `CloseAllConnections` 涓€璧锋竻闆讹紙鐜版湁鍥炶皟锛屾棤闇€鏂颁唬闄?API锛夈€?
**鍙湁杩欎簺绠?H3 浼犺緭澶辫触**锛坄isH3TransportFailure`锛夛細`{Handshake, Path}` 涓斾笉鏄秴鏃讹紱`cronet.NetError` 鐨?QUIC 鍗忚/鎻℃墜鏃忋€?*407 / 鍏跺畠鐘舵€?/ 鍙栨秷 / EOF / 瓒呮椂涓€寰嬩笉闄嶇骇**銆?
**蹇呴』鍚屾椂鍐欑殑娴嬭瘯**锛坒ake client锛屾棤闇€ Native锛夛細UDP 鎷掔粷涓庨粦娲為兘闄嶇骇涓€娆°€丠3 鎴愬姛銆佹參 200锛坅mbiguous 涓嶅啓 broken锛夈€?07 涓嶉檷绾с€丠2 涔熷潖銆乧aller cancel銆佺綉缁滃垏鎹㈢珵鎬侊紙-race锛夈€丆lose 鍚?Dial銆佺獥鍙ｅ埌鏈熷鑻忋€乧ached 棣栧寘鍦烘櫙涓嶉噸鏀俱€丄lt-Svc 璐熷悜瀵圭収锛堝悓 Engine 杩炵画 H3鈫扝2鈫扝3 鐨勭湡瀹?negotiated protocol锛夈€?
**榛樿鍊?*锛歚quic_fallback_to_h2: false`锛沗quic:false` 涓?`quic:true` 鐨勭幇鏈夎涔夊畬鍏ㄤ笉鍙橈紱鍛藉悕寰呮寜 `option/naive.go` 鐨勭浉閭诲瓧娈甸鏍煎畾绋裤€?
### 6.3 涓轰粈涔堣繖浠嶅€煎緱鍋氾紙鍙嶆柟璇佹嵁涓€骞跺垪鍑猴級

- 鍙嶆柟锛歯aiveproxy #669 缁存姢鑰呮槑纭嫆缁?鍚岄厤缃贩鐢?HTTPS/QUIC"锛屼富寮犵敱瀹㈡埛绔寚瀹氬崗璁紱#756 浣滆€呬篃璇?QUIC 涓嶆瘮 TLS 蹇€?- 姝ｆ柟锛氳疆瀛愬凡缁忚瘉鏄?forced-H3 鍦?UDP 琚嫆/榛戞礊鏃?4.0 s 澶辫触涓?*涓嶄細**鍥炶惤锛岃€屽悓绔彛 H2 6 ms 鍙敤锛涜繖鏄?*鍙敤鎬?*缂哄彛锛屼笉鏄€熷害鍗栫偣銆傚仛鎴愰粯璁ゅ叧闂殑鍙€夐」锛屼笌缁存姢鑰呯殑"瀹㈡埛绔帶鍒?骞朵笉鍐茬獊銆?
---

## 7. 涓庡弬鑰冨疄鐜扮殑瀵圭収锛氭湰杞鍒颁簡浠€涔堛€侀噰鐢ㄤ簡浠€涔堛€佹嫆缁濅簡浠€涔?
| 鍙傝€?| 缁撹 | 鏈疆鍔ㄤ綔 |
|---|---|---|
| Caddy forwardproxy锛坄d62c80d3`锛夌殑 CONNECT 娴佺▼ | 鍏?`200 + Flush`锛屽啀鎷ㄧ洰鏍囷紱H1 raw銆丠2/H3 padded锛沗Padding` 鍝嶅簲澶存棤鏉′欢 | **鏍稿鍚庣‘璁ゆ垜浠病鏈夊亸宸?*锛汸1 鐨勬彙鎵嬬瓑寰呮濂藉彂鐢熷湪 200 涔嬪悗锛屼笌鍙傝€冪殑 200-before-dial 鐩稿 |
| naiveproxy 瀹樻柟瀹㈡埛绔?| 浼氱瓑 CONNECT 200锛坄naive_proxy.cc`/`preamble` 璺緞锛夛紝407 鏄‖閿欒 | **閲囩撼**锛氭垜浠箣鍓嶄笉绛夛紝杩欐槸鐩稿鍙傝€冪殑琛屼负缂洪櫡锛汸1 淇殑灏辨槸瀹?|
| naiveproxy #669锛?024-10-01锛宯ot planned锛?| "搴旇鐢卞鎴风鎸囧畾鍗忚锛岃繖鏍峰埄浜庢帶鍒舵€ц兘" | **閲囩撼涓?P3 鐨勯粯璁ゅ€肩害鏉?*锛氶粯璁ゅ叧闂€佹樉寮忓紑鍚?|
| naiveproxy #756 | QUIC 鏈壒鍒紭鍖栵紱浣滆€呮嫆缁濇縺杩?CC | **涓嶉噰绾?*浠讳綍 QUIC 璋冨弬锛汸8 缁存寔榛樿 |
| cronet-go #11 | `QUIC=false` 鏇句笉浼犳挱 `enable_quic`锛孉lt-Svc 鍙妸 H2 鎮勬倓鍙?QUIC | 宸茬敱 pin 淇锛涘啓杩?P3 鐨?鏃?`-force-quic` 鈮?H2"绾︽潫 |
| cronet-go #6 / sing-box #4002 | padding 鏈竻闆舵硠婕忔棫鍐呭瓨 | **P9**锛氳ˉ 256 鎶芥牱鐨勫洖褰?+ 鍙樺紓楠岃瘉 |
| sing-box #4205 / #4249/#4287 / #4458 | ALPN race銆丠2 wrapper銆丠TTP2Options | 璧风偣宸插惛鏀讹紱**鏈洖閫€浠讳綍宸叉湁澧炲己** |
| sing-box #3803 / #4620 | DoH bootstrap 缁曡繃 DNS 绛栫暐 | **P2**锛氬疄娴嬬‘璁や粛鍦紱鏃?Go 渚у紑鍏?鈫?`BLOCKED_BY_NATIVE_API` |

---

## 8. 鎻愪氦銆佹帹閫佷笌楠岃瘉

### 8.1 鎻愪氦锛? 涓紝鍧囧彧鍚湰杞枃浠讹級

| SHA | 涓婚 |
|---|---|
| `e9a7cbcad` | `test(naive): pin that the padding region is zeros on the wire` |
| `b3ee59151` | `docs(naive): state the real quic_congestion_control default` |
| `1fb42b5f5` | `fix(naive): complete the CONNECT handshake and report failure facts` |
| `84760a372` | `docs(naive): record the round-one performance and integration construction`锛堟湰鎶ュ憡锛?|

### 8.2 鏈疆鐪熸璺戣繃鐨勯獙璇?
| 鍛戒护 | 缁撴灉 |
|---|---|
| `go test -tags "$(OTHERS)" ./protocol/naive/... ./transport/http/ ./common/httpclient/ ./protocol/group/ ./adapter/` | **PASS**锛坣aive 0.35s / quic 0.04s / transport/http 25.3s / httpclient 0.02s / group 3.9s / adapter 0.7s锛?|
| `go test -tags "$(WINDOWS)" ./protocol/naive/` | **PASS**锛堝惈鏂扮殑 14 琛?facts 鐭╅樀 + 4 涓?dial 娴嬭瘯 + 鏃㈡湁 dependency_pin 娴嬭瘯锛?|
| `go vet -tags "$(WINDOWS)" ./protocol/naive/` | **clean** |
| `gofmt -l protocol/naive/` | **绌?* |
| 鍙樺紓楠岃瘉锛坄WriteZeroN`鈫抈Extend`锛?| **鎸夐鏈熷彉绾?*锛屾仮澶嶅悗缁?|
| 绔埌绔?407 / 200锛堢湡瀹?Native + 鐪熷疄 sing-box 鍑虹珯锛?| **407 鍙橀敊璇紙curl 502锛?*锛?*200 姝ｅ父** |
| Native 杩愯鏃剁増鏈?| `154.0.8037.49` |
| `-race` | **鏈窇**锛圵indows + cronet 鐨勫凡鐭ラ檺鍒讹紱P1 鏂板閫昏緫鏄函鍒嗙被涓庡寘瑁咃紝鏃犲叡浜姸鎬侊紱骞跺彂娴嬭瘯鐣欑粰绗簩浠?Debug 鍦ㄥ彲鐢ㄥ钩鍙版墽琛岋級 |
| macOS/iOS 鏋勫缓 | `NOT_RUN`锛堟棤鐜锛?|

### 8.3 鎺ㄩ€?
鎺ㄩ€佸墠閲嶆柊 `git fetch origin testing` 姣斿锛堢粨鏋?`3 0`锛岃繙绔湭鍔級锛涗互 fast-forward 鎺ㄥ埌鐜版湁 `testing`锛?
```
11c4c1b27..84760a372  testing -> testing
```

鎺ㄩ€佸悗鍐嶆 fetch锛宍HEAD...origin/testing` = `0 0`锛宍origin/testing` 澶?= `84760a372`銆傛湭 force銆佹湭 rebase 浠讳綍浠栦汉鎻愪氦銆?
---

## 9. KEEP / REJECTED / NOT_RUN锛堟湰杞槑纭笉鍋氱殑锛?
| 椤?| 鐘舵€?| 鐞嗙敱 |
|---|---|---|
| 鏈嶅姟绔?Go inbound 閲嶅啓 | **KEEP** | 鏈疆娌℃湁鎵惧埌鍙綊鍥犵殑鏈嶅姟绔摱棰堬紱鏃㈡湁 codec 鏈夊疄娴嬪畾閲忥紙搂闄勫綍锛?|
| P4 闈?CONNECT 鐘舵€佺爜 | **KEEP锛堜笉鏀癸級** | 鍙傝€冩槸浜ょ粰 next handler锛屼笉鏄浐瀹?407锛涙満姊版敼鎴?407 鏄弽鎺㈡祴閫€姝?|
| P5 瀹㈡埛绔浂鏁版嵁甯?| **KEEP锛堜笉鏀?pin锛?* | 鍙揪鎬ф瀬浣庯紝鏈嶅姟绔凡纭寲 |
| P6 瀹㈡埛绔?padding 澶村垎甯?| **KEEP锛堜笉鏀癸級** | 鎴戜滑鐨勬湇鍔＄涓?Caddy 閫愬瓧鑺備竴鑷达紱鍙湁瀹㈡埛绔笌瀹樻柟瀹㈡埛绔笉鍚岋紝涓旀棤浜掗€氳瘉鎹?|
| P8 Engine/鎺ユ敹绐楀彛榛樿 | **KEEP** | 绌洪棽鎬佹暟鎹笉瓒充互缈昏浆榛樿锛涙椿璺冭礋杞介渶 macOS + 鐪熷疄閾捐矾 |
| P2 MAP 琛ヤ竵 | **REJECTED** | 娌讳笉浜?stray 杩炴帴锛屽睘浜?鐪嬭捣鏉ヤ慨濂戒簡" |
| 鑷淮鎶?Chromium patch 鍏?DoH | **NOT WORTH IT** | 缁存姢鎴愭湰楂樹簬涓€鏉″惎鍔ㄦ湡杩炴帴鐨勪环鍊?|
| P3 | **DEFERRED** | 闇€瑕?cronet-go 鍒嗘敮 + 閲嶆柊 pin + 鐙珛瀵规姉鐭╅樀锛埪?锛?|

---

## 10. 闄勶細鏈疆鍙紩鐢ㄧ殑瀹炴祴鏁版嵁

**鏃㈡湁鍩虹嚎锛堝璁℃湰杞娴嬶紝Windows/amd64锛宭oopback锛屼粎璇存槑 Go 灞傛垚鏈級**

| Benchmark | ns/op | allocs/op |
|---|---|---|
| `PaddingWriteBuffer/framed 1400B` | ~285 | **0** |
| `PaddingWriteBuffer/framed 16KiB` | ~258鈥?88 | **0** |
| `CachedFirstPayload/buffer 1400B` | ~316鈥?36 | **0** |
| `CachedFirstPayload/buffer 16KiB` | ~308鈥?42 | **0** |
| `PaddingWriteChunked 1400B` | ~5.2鈥?.4 碌s | 4 |

**P1 绔埌绔紙鏈満锛屽惈寮曟搸鍚姩鐨勬柊杩涚▼锛?*锛氭纭瘑鐮?200锛涢敊璇瘑鐮?407 鍦?9鈥?0 ms 鍐呰繑鍥烇紙`response received, protocol: h2, status: 407`锛夈€傝繖涓嶄唬琛?WAN銆?
---

## 11. 浜ょ粰绗簩浠?Debug 鎻愮ず璇嶇殑杈撳叆

- **base SHA**锛歚11c4c1b27ff87a6ee686c9c1b578a333bbb726d4`
- **鏂藉伐 commits**锛歚e9a7cbcad`銆乣b3ee59151`銆乣1fb42b5f5`
- **涓や釜浠撳簱鏈€缁?SHA**锛歴ing-box = 鎺ㄩ€佸悗鐨?`testing` 澶达紱cronet-go **鏈敼**锛堜粛 `8c68ce89873c876鈥锛?- **瑙︾鐨勭湡瀹炲嚱鏁?*锛歚dialNaiveTunnel`銆乣naiveHandshakeFailureFacts`銆乣naiveResponseStatus`銆乣(*Outbound).DialContext`銆乣(*naiveDialer).DialContext`銆乣writeFrame`/`writeBufferWithPadding`/`writeChunked`锛堜粎娴嬭瘯锛?- **娴嬭瘯鍏ュ彛**锛?  - `go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./protocol/naive/... ./transport/http/ ./common/httpclient/ ./protocol/group/ ./adapter/`
  - `CGO_ENABLED=0 go test -tags "$(cat release/DEFAULT_BUILD_TAGS_WINDOWS)" ./protocol/naive/`锛堥渶瑕?`libcronet.dll` 鍚岀洰褰曟椂鎵嶇湡姝ｅ姞杞?Native锛涚函鍒嗙被娴嬭瘯涓嶉渶瑕侊級
  - 绔埌绔細`sing-box-client.exe run -c client-{good,bad}.json` + `curl -x http://127.0.0.1:18081 http://127.0.0.1:18080/`
- **灏氭湭瑕嗙洊 / 寤鸿鏀诲嚮**锛?  1. P1 鐨?**+1 RTT**锛氱敤 120 ms 閾捐矾閲忓寲棣栧瓧鑺備笌 TTFB 鍥炲綊锛涚‘璁?`kickWriteHandshake`/cached 棣栧寘涓庢柊鎻℃墜鐨勬椂搴忔棤閲嶅绛夊緟銆?  2. 闈?407 鐘舵€佺爜鏄惁搴斾骇鐢?fact锛堥渶瑕佺湡瀹?Caddy 403/502 鍦烘櫙锛夈€?  3. `naiveResponseStatus` 瀵逛緷璧栨枃妗堢殑鑰﹀悎锛氬姞涓€鏉¤法浠?pin 鏂█銆?  4. P9 鐨勯浂濉厖娴嬭瘯鍦?`NaiveH2Conn` 灞傦紙flush 璺緞锛夎繕闇€瑕佷竴鏉＄鍒扮鏂█銆?  5. P3 钀藉湴鏃剁殑骞跺彂/绔炴€佺煩闃碉紙搂6.2锛夈€?  6. DoH stray锛氬湪鍙屾爤涓?Google 鍙揪鐨勭綉缁滀笂纭瀹冩槸鍚︾湡鐨勪骇鐢?DNS 浜嬪姟锛堟湰杞彧鐪嬪埌 socket 灏濊瘯锛夈€?
