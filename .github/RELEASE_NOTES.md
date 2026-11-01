本版主要改进 Apple 构建和 TestFlight 发布流程，适合在 Windows 开发、由 Mac 完成签名与上传的使用方式。

- **Mac 可以提前开始 TF 打包**：Windows 启动 Apple client Actions 后，Mac 可立即跟随指定运行。Libbox 就绪后即可开始归档，无需等待所有平台或正式 Release 页面。
- **TF 与正式 Release 使用相同版本和核心**：固定核心提交、Apple 客户端提交、版本号和 Libbox 产物；正式发布通过运行 ID 与重跑次数选择同一份库，避免相同核心提交的其他构建被误选。
- **上传前再次核对**：对应平台的 CI 通过、远端标签指向正确提交后才上传 TF；同时检查归档后的主应用和扩展的版本号、构建号。
- **减少重复工作**：旧入口复用同一提交正在运行的构建；单平台发布跳过另一平台的准备；Apple 嵌套依赖在选定最终提交后只初始化一次。
- **macOS 单运行器也先提供库**：Libbox 完成后先上传产物，随后继续构建客户端，供签名 Mac 提前准备。
- **修复仓库识别**：从 origin 明确解析发布仓库，避免同时存在 upstream 远端时 GitHub CLI 默认指向上游项目。

Mac 命令示例（将运行 ID 换成本次 Apple client 运行）：

```sh
./scripts/publish-apple-beta.sh --follow-run 123456789 --platform ios
```

正式 Release 的 `apple_run_id`、`apple_run_attempt` 应与 Mac 跟随的运行一致。完整步骤见 [Apple / TestFlight 发布说明](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/v0.1.36/docs/APPLE-BETA-RELEASE.md)。

本版未更改代理内核的配置语法或数据处理逻辑。TestFlight 签名和上传仍在用户自己的 Mac 上进行。
