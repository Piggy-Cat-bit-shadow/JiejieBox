# JiejieBox

基于 [sing-box](https://github.com/SagerNet/sing-box) 的代理内核，面向服务端和多平台客户端。JiejieBox 在上游的协议、路由与 DNS 基础上，增加传输、选路和生命周期管理能力，并维护配套客户端与构建流程。

[下载与版本](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) · [更新日志](CHANGELOG.md) · [配置文档](docs/configuration) · [Fork 差异](docs/FORK-DIFF.md) · [知识库](KNOWLEDGE/README.md)

## 获取与使用

前往 [GitHub Releases](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) 选择适合平台的版本。以 v0.1.34 为例，发布页包含 Linux amd64、Windows amd64、macOS arm64、Android arm64 内核、Apple Libbox、Windows 客户端安装程序及 `SHA256SUMS`。各版本的实际文件以对应发布页为准。

Android 图形客户端：[satelite-one](https://github.com/Piggy-Cat-bit-shadow/satelite-one)。Apple、Android 和桌面端源码的版本由本仓库的 [子模块](.gitmodules) 固定；客户端和 Libbox 应使用配套版本。

内核使用 JSON 配置。准备好配置文件后，可先检查再运行：

```sh
sing-box check -c config.json
sing-box run -c config.json
```

Windows 下将 `sing-box` 换成实际的 `sing-box.exe` 路径。配置格式和基础概念可参考 [sing-box 官方文档](https://sing-box.sagernet.org/)，本仓库额外能力请查看下方文档。配置中的服务器地址、凭据与路由策略需要根据自己的环境填写。

## 主要能力

- **协议与传输**：在上游功能基础上，提供 XHTTP 客户端传输、Mieru 客户端及服务端、Native Naive 入站和 MASQUE 相关能力。Naive 出站等功能受目标平台和构建标签影响。参见 [Mieru](docs/fork/mieru-integration.md)、[Naive](docs/naive.md)、[MASQUE](docs/masque.md) 和 [XHTTP](docs/configuration/shared/v2ray-transport.md)。
- **选路与代理组**：提供 `loadbalance` 组、可选的有限故障切换和启动时路径校验；负载均衡按连接或 UDP 会话选取成员。参见 [负载均衡](docs/fork/load-balance.md)。
- **DNS 与网络切换**：维护 DNS 分组、缓存与网络代次隔离，以及休眠、唤醒和接口切换时的资源管理。实现细节见 [DNS 知识库](KNOWLEDGE/modules/dns-demand.md) 和 [移动端生命周期](KNOWLEDGE/modules/power-mobile-lifecycle.md)。
- **数据路径**：支持按需启用的上传整形与流量分类，并对缓冲区及直连路径进行优化。Direct Offload 的实际数据路径绕行仅适用于 Linux `auto_redirect`；TUN 模式不会因此绕过用户态。参见 [流量调度](docs/fork/traffic-scheduler.md) 和 [Direct Offload](docs/fork/direct-offload.md)。
- **客户端集成**：通过 Libbox 与移动端、桌面端集成，提供平台构建配置及发布校验流程。参见 [构建配置](docs/BUILD-PROFILES.md) 和 [客户端接口](KNOWLEDGE/modules/libbox-platform-contract.md)。

能力是否包含在某个二进制文件中取决于其平台、版本和构建标签。上游文档不能代替本 fork 的配置说明：例如 `loadbalance`、`traffic_scheduler` 和 `traffic_class` 是 fork 扩展，使用这些字段的配置不能直接交给官方 sing-box 加载。

## 从源码构建

当前 `go.mod` 要求 Go 1.25.5 或更新的兼容工具链。先克隆仓库并进入目录：

```sh
git clone https://github.com/Piggy-Cat-bit-shadow/JiejieBox.git
cd JiejieBox
```

在 Linux 上构建项目的 amd64 服务端配置：

```sh
./scripts/ci/build-server.sh linux amd64 dist/sing-box-linux-amd64
```

在 macOS 上构建 arm64 内核配置：

```sh
./scripts/ci/build-macos-client.sh arm64 dist/sing-box-darwin-arm64
```

两个脚本分别读取对应的构建标签。Windows 和移动端构建需要各自的平台工具链及标签；其中 `release/DEFAULT_BUILD_TAGS` 是 Darwin 配置，不应直接用作 Windows 构建配方。构建客户端时还需初始化所需子模块，并遵循对应客户端的构建说明。更多细节见 [构建配置](docs/BUILD-PROFILES.md)、[源码构建](docs/installation/build-from-source.md) 和 [关联仓库](FORK_REPOSITORIES.md)。

## 文档与开发

- [配置参考](docs/configuration) · [上游迁移说明](docs/migration.md) · [上游更新日志](docs/changelog.md)
- [服务端](docs/JIEJIE-SERVER.md) · [macOS 客户端](docs/JIEJIE-MACOS-CLIENT.md) · [Windows 状态](docs/WINDOWS-STATUS.md)
- [Fork 差异](docs/FORK-DIFF.md) · [工程笔记](docs/ENGINEERING-NOTES.md) · [知识库索引](KNOWLEDGE/INDEX.md)
- [提交问题](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/issues)：请提供版本、平台、复现步骤和脱敏后的配置或日志。

知识库记录了特定源码基线及其证据范围，阅读前请先查看 [基线说明](KNOWLEDGE/BASELINE.md)，并以当前源码和构建配置为准。[旧版首页资料](docs/archive/jiejiebox-original-readme.md) 已存档，其中性能测试结果取决于测试环境与场景，不代表所有设备或网络的实际表现。

## 致谢与许可

感谢 [SagerNet/sing-box](https://github.com/SagerNet/sing-box) 及所有上游项目和贡献者。JiejieBox 基于 [GNU General Public License v3.0 或更新版本](LICENSE) 发布；依赖及配套项目的许可请参阅各自仓库。
