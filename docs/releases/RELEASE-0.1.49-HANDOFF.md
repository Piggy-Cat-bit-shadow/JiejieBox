# v0.1.49 发布交接

日期：2026-11-14，Asia/Shanghai。

- 内核正式 tag：`v0.1.49`，剥离提交 `4e2b5d9f26db998c90290797f41439b1e41fcd89`。
- [内核 Release](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.49)：非 draft、非 prerelease，8 项契约附件及正文已回读。
- [Kernel release 43588752879](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43588752879)：freeze、contract、Verify/race、全部平台、stage、publish 成功。
- Apple 固定 `749509f214d97f43cbc66f903fd17b0b50eb6399`。三处 Swift 初始化器调用补齐 report 参数标签；预先双平台编译运行 43584135709 成功，正式批次 iOS/macOS/自签也成功。
- Libbox 身份绑定上述内核 SHA、Apple SHA、run 43588752879 / attempt 1；摘要在公开 Libbox manifest 和 SHA256SUMS 中。
- [Satelite Release](https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.49)：客户端 `3e27e14d871a7a019e1ca2c290b3307ba2939003`，versionCode 60，coreCommit 等于正式内核 SHA。
- [Satelite run 43591411922](https://github.com/Piggy-Cat-bit-shadow/satelite-one/actions/runs/43591411922) 成功；双 ABI 签名 APK、SHA256SUMS.txt、build-info.json、apksigner-verify.txt 已回读，非 draft、非 prerelease。
- 根 CHANGELOG 与内核 Release 正文已补齐；文档提交使用 skip ci，不移动 tag。
- 已删除合并完成且无工作树检出的远端 work/resource-governance-closure-2026-11-14，主仓仅剩 testing。
- 首次正式编排 43587643030 在 freeze 被版本文件 CRLF 阻断，平台任务尚未启动；改回 LF 后重新验证、准备和正式发布，未移动任何已公开 tag。
- TestFlight 未在本次 Windows 发布任务执行，后续从 TESTFLIGHT-MAC.md 进入。

总任务起点未精确记录，不能以最终成功运行时长代替总耗时；各次准备、验证、失败与成功运行的时间以链接中的 Actions 时间为准。
