# 10｜command.sock 路径二次修复（真机 110 字节）+ Darwin 真实 bind 证据

## 第二次真机失败

```text
start command server error: listen command server:
socket path is 110 bytes, the kernel allows 103:
/private/var/mobile/Containers/Data/PluginKitPlugin/<UUID>/tmp/jjb-xxxxxxxx.sock
```

第一次修复（`reports/06`）把 socket 换到 `os.TempDir()`，假设系统临时目录很短。在 iOS Packet Tunnel
里 `TMPDIR` **就是容器自己的 tmp**：92 字节，加 17 字节文件名就是 110。**同一个机制换了个位置复现**，
所以 `reports/06` 的"换目录"方案已被本文件取代。

## 新设计

不再"换一个目录"，而是把目录长度当输入、按剩余预算决定文件名（`experimental/libbox/command_socket.go`）：

- 候选顺序：basePath → 平台 temp（`sTempPath`）→ 容器根（比 tmp 短 4 字节）；
- 名字随预算收缩：basePath 保持 `command.sock`；其它目录用 `h<hex>.sock`，hash 8→4 位；
- 都放不下时返回带字节数的错误，**不含容器路径**；
- 服务端与客户端同一函数；清理只删自己生成的名字（`command.sock` / `h*.sock`）。

本次真机布局的答案：容器根 + 4 位 hash = **99 字节**。

## 测试升级（上一轮真正的漏洞）

上一轮 5 个测试只证明"开发机上临时目录够短"，无法复现 110。新增单测直接吃真机报告的三条路径常量：
先断言旧 fallback 确实是 110 字节，再断言新解析落在容器根、99 字节、名字合法；边界 103/104；
多字节目录按字节；无解报长度不报路径；确定性与互异性；只删自己名字。

**Darwin 真实 bind**（`command_socket_darwin_test.go`，experiment workflow 的 macOS runner）：

```
--- PASS: TestDarwinBindsTheResolvedPathInADeepContainer (0.00s)
--- PASS: TestDarwinRejectsAPathOverTheLimit (0.00s)
ok  github.com/sagernet/sing-box/experimental/libbox  0.043s
```

第一版这个测试用 `t.TempDir()`，macOS 上它本身深到装不下容器形状，于是因为 fixture 失败；
`d73534693b3` 改为在 `/tmp` 下构造，才真正测到解析器。

另一处**由 CI 抓出来的测试缺陷**：`TestTheByteBoundaryIsExact` 在 Linux 的 low-memory 门禁里失败，
原因是断言写成了"不能用超限的目录"，而正确不变量是"最终路径必须可绑定"（解析器保留目录、缩名字是对的）。
`c2031010047` 修正断言，Mobile contract 转绿。

## 最终产物

| 项 | 值 |
|---|---|
| core | `c203101004743f4d0883153aaa0b0a93efea64c2` |
| apple | `4773b5fb9802f8d7c91f4af2962d35879399b56f` |
| CI | run `41815286485`（ios + macos 全绿，含 Darwin socket 步骤） |
| full IPA | `512a3e2736ed6f80c4692282d7ab6827c35bc7f9717b7820d8ff33544583a807` |
| diagnostic IPA | `300daa7f4ef0076310c2f7e728652ed6fca781eaf6db844adbc2940a379a31b9` |
| Verify / Mobile contract / Android Core | `41815286485` 同批全部 success |

## 仍不宣称

真机：隧道启动后能否联网、无 App Group 时统计/命令通道不可用、本地续签回环是否真被 TUN 应答。
本轮没有改 Apple 仓库，也没有碰 `client-apple.yml` / Release / TestFlight；未建新分支。
