# Round 1 Dashboard 95 个上游提交映射

## 结论

- 上游 `SagerNet/sing-box-dashboard:main` @ `3e4bf08e5bf5b9417cf9c36e5a2d5abcb21bdd34`，共 **95 个提交**；fork 主线 merge `b592c65618e13e2c7ea24a06e758db72c71f65be`（parents `fc1dbb4` + `3e4bf08`），消费线 `9bde90e1cd5577dc3f5549535aa23bea69816d6f`。
- 95/95 有映射，无未归因提交。按最终树比较：**27 个提交完全一致**、**61 个提交的变更文件已被 fork 编辑/融合**、**7 个提交涉及重命名/被 fork 取代或上游子模块移除**。
- 最终树与上游只差 **22 个文件**：其中 8 个是 fork-only 的配置/远端更新/订阅配额功能与其测试，14 个是品牌、API/配置、构建配置的 fork 差异；没有上游独有文件缺失。
- 测试：main `pnpm test` 97/97，消费线 98/98；`pnpm build` PASS；`pnpm lint` PASS。`lint:css` 的 4 个错误是 fork-only `ProfileViews.module.css` 既有规则，登记为 BASELINE_CONFIRMED，未通过禁用规则伪装通过。

## 主题分组

| 主题 | 提交数 | 代表提交 | 当前证据 |
|---|---|---|---|
| Other | 27 | 0ff3e359171e Init commit；e7f195993662 Minor fixes；a65bca078b75 Minor fixes | 其余 UI/行为提交，按 95 行表逐项列出 |
| Desktop shell | 26 | 9ab68deb50e4 Refactor menu picker；75fca1058600 Add desktop files；d447d5faccb2 Fix desktop-host gating and web regressions | desktop.ts、tray/窗口/菜单现有实现 |
| Settings / profiles | 10 | f679da9d40d5 Move server management to a settings sub-page；15077b844fc9 Remove the icon from server list items；449ce810e89c Improve Settings | SettingsView / ProfileViews、desktop.ts |
| Dependencies / build | 8 | a6b51a1416f9 Update sing-box；9748b21caab0 Update dependencies；e5ccc9573775 Fix Maximum update depth exceeded in logs under message bursts | package.json/vite/CI，pnpm install/build PASS |
| Tailscale / TailSSH | 5 | 9170740cab15 Improve Tailscale interface；809e215eab9a Improve Tailscale features & Minor fixes；af3900a94e22 Fix missing auth banner in tailssh | protocol/Tailscale UI、tailssh auth banner；现有 Tailscale 页面与测试 |
| Connections / groups / urltest | 5 | 955a86c92543 Render tray groups as a stable three-level cascade；f2e702cfd798 Fix urltest；9242620c8395 Improve groups | ProfileViews、GroupsView、urltest 卡片；97/97 tests |
| Setup / onboarding | 3 | 8b4cfd1c6f95 Redesign setup screen footer；95ff916818e3 Add theme picker to the setup screen & Rework its layout；cd2801a1939b Finish aligning desktop setup and settings | SetupView / SettingsView 现有路由 |
| UI / theme / layout | 3 | b9a88c2ebc5a Add theme picker & Refactor server picker；f16b9e22d8de Refactor css；d8e174b74431 Drop the app version and speed rows and restyle the settings actions | App.tsx、globals.css、组件样式；build PASS |
| Mobile | 2 | 33aa70f87d89 Improve mobile experience；07976642d276 Fix render icon glyphs at correct size on old iOS Safari | App.tsx mobile topbar / 响应式布局 |
| Terminal | 2 | ccc79a764a66 desktop: Fix missing copy/paste for terminal；e5b293de743b desktop: Fix terminal layout | src/views/TerminalView.tsx、TerminalSessionLayout.tsx、terminalThemes/terminalKeys |
| i18n | 1 | 699d2a15c851 Stop translating STUN test result values | translations.ts 429 keys（上游 425 的子集关系） |
| PWA / offline | 1 | d8e677f9bffc Add PWA support | vite PWA 配置、sw 生成；build PASS |
| USB/IP | 1 | 187a199d163c Add USB/IP service support | src/api/daemon.ts、SettingsView 的 USB/IP 配置 |
| Taildrop | 1 | 4f29dcc7b932 Add Taildrop support | src/app/desktop.ts / ProfileViews.tsx 的 Taildrop 入口；97/97 tests |

## 95 个提交逐项映射

| # | commit | date | subject | status | files(exact/edited/superseded) | notes |
|---|---|---|---|---|---|---|
| 1 | `0ff3e359171e` | 2026-06-12 | Init commit | PRESENT_EXACT | 0 (0/0/0) |  |
| 2 | `8b4cfd1c6f95` | 2026-06-12 | Redesign setup screen footer | PRESENT_WITH_FORK_EDITS | 6 (2/4/0) | edited:src/App.tsx; edited:src/app/translations.ts; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 3 | `9170740cab15` | 2026-06-12 | Improve Tailscale interface | PRESENT_RENAMED_OR_SUPERSEDED | 14 (9/4/1) | edited:src/App.tsx; edited:src/app/translations.ts; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/TerminalDialog.tsx->src/views/TerminalView.tsx; removed-intentional:tsconfig.tsbuildinfo |
| 4 | `e7f195993662` | 2026-06-12 | Minor fixes | PRESENT_WITH_FORK_EDITS | 7 (3/4/0) | edited:index.html; edited:src/App.tsx; edited:src/app/translations.ts; edited:src/styles/global.css->src/styles/globals.css |
| 5 | `b9a88c2ebc5a` | 2026-06-12 | Add theme picker & Refactor server picker | PRESENT_WITH_FORK_EDITS | 9 (4/5/0) | edited:index.html; edited:src/App.tsx; edited:src/app/translations.ts; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 6 | `f679da9d40d5` | 2026-06-12 | Move server management to a settings sub-page | PRESENT_WITH_FORK_EDITS | 3 (0/3/0) | edited:src/App.tsx; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 7 | `699d2a15c851` | 2026-06-12 | Stop translating STUN test result values | PRESENT_WITH_FORK_EDITS | 3 (2/1/0) | edited:src/app/translations.ts |
| 8 | `95ff916818e3` | 2026-06-12 | Add theme picker to the setup screen & Rework its layout | PRESENT_WITH_FORK_EDITS | 5 (2/3/0) | edited:src/App.tsx; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 9 | `a65bca078b75` | 2026-06-12 | Minor fixes | PRESENT_RENAMED_OR_SUPERSEDED | 27 (23/3/1) | edited:.github/workflows/ci.yml; edited:src/App.tsx; edited:src/styles/global.css->src/styles/globals.css; removed-intentional:tsconfig.tsbuildinfo |
| 10 | `3c21f1f741fc` | 2026-06-12 | Minor refactors | PRESENT_WITH_FORK_EDITS | 21 (17/4/0) | edited:src/App.tsx; edited:src/app/translations.ts; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 11 | `15077b844fc9` | 2026-06-12 | Remove the icon from server list items | PRESENT_WITH_FORK_EDITS | 1 (0/1/0) | edited:src/views/SettingsView.tsx |
| 12 | `a6b51a1416f9` | 2026-06-12 | Update sing-box | PRESENT_RENAMED_OR_SUPERSEDED | 1 (0/0/1) | removed-intentional:sing-box |
| 13 | `228f37b0a29a` | 2026-06-12 | Deploy to GitHub Pages on push to main | PRESENT_WITH_FORK_EDITS | 1 (0/1/0) | edited:.github/workflows/deploy.yml |
| 14 | `16b397030584` | 2026-06-12 | Make back buttons icon-only with a larger arrow | PRESENT_WITH_FORK_EDITS | 4 (2/2/0) | edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 15 | `33aa70f87d89` | 2026-06-13 | Improve mobile experience | PRESENT_RENAMED_OR_SUPERSEDED | 16 (10/5/1) | edited:index.html; removed-intentional:sing-box; edited:src/App.tsx; edited:src/api/daemon.ts; edited:src/app/translations.ts |
| 16 | `86ddcca826e1` | 2026-06-13 | Accept an injected transport in DaemonApi | PRESENT_WITH_FORK_EDITS | 1 (0/1/0) | edited:src/api/daemon.ts |
| 17 | `449ce810e89c` | 2026-06-13 | Improve Settings | PRESENT_WITH_FORK_EDITS | 8 (4/4/0) | edited:src/App.tsx; edited:src/app/translations.ts; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 18 | `9b30024481e8` | 2026-06-13 | Cleanup comments | PRESENT_WITH_FORK_EDITS | 34 (29/5/0) | edited:src/App.tsx; edited:src/api/daemon.ts; edited:src/app/translations.ts; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 19 | `a6f5c83a899c` | 2026-06-13 | Create release branch | PRESENT_WITH_FORK_EDITS | 2 (0/2/0) | edited:.github/workflows/deploy.yml; edited:vite.config.ts |
| 20 | `9748b21caab0` | 2026-06-13 | Update dependencies | PRESENT_EXACT | 5 (5/0/0) |  |
| 21 | `809e215eab9a` | 2026-06-13 | Improve Tailscale features & Minor fixes | PRESENT_RENAMED_OR_SUPERSEDED | 25 (18/4/3) | removed-intentional:sing-box; edited:src/App.tsx; edited:src/app/translations.ts; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 22 | `0970326306cc` | 2026-06-13 | Add live server reachability indicator | PRESENT_WITH_FORK_EDITS | 14 (9/5/0) | edited:src/App.tsx; edited:src/api/daemon.ts; edited:src/app/translations.ts; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 23 | `9ab68deb50e4` | 2026-06-13 | Refactor menu picker | PRESENT_WITH_FORK_EDITS | 6 (4/2/0) | edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx |
| 24 | `07976642d276` | 2026-06-15 | Fix render icon glyphs at correct size on old iOS Safari | PRESENT_EXACT | 1 (1/0/0) |  |
| 25 | `3adbbe7938ff` | 2026-06-15 | Fix Anadroid tap highlight | PRESENT_WITH_FORK_EDITS | 1 (0/1/0) | edited:src/styles/global.css->src/styles/globals.css |
| 26 | `c48aeab20ba9` | 2026-06-15 | Improve server probe | PRESENT_EXACT | 1 (1/0/0) |  |
| 27 | `8096ca70c55d` | 2026-06-15 | Unified tools title | PRESENT_EXACT | 1 (1/0/0) |  |
| 28 | `d8e677f9bffc` | 2026-06-16 | Add PWA support | PRESENT_RENAMED_OR_SUPERSEDED | 9 (5/3/1) | edited:index.html; edited:src/api/daemon.ts; removed-intentional:vendor/sing-box; edited:vite.config.ts |
| 29 | `187a199d163c` | 2026-06-17 | Add USB/IP service support | PRESENT_WITH_FORK_EDITS | 16 (12/4/0) | edited:src/App.tsx; edited:src/api/daemon.ts; edited:src/app/translations.ts; edited:src/styles/global.css->src/styles/globals.css |
| 30 | `af3900a94e22` | 2026-06-19 | Fix missing auth banner in tailssh | PRESENT_WITH_FORK_EDITS | 2 (1/1/0) | edited:src/styles/global.css->src/styles/globals.css |
| 31 | `f16b9e22d8de` | 2026-06-19 | Refactor css | PRESENT_WITH_FORK_EDITS | 39 (34/5/0) | edited:.github/workflows/ci.yml; edited:src/App.tsx; edited:src/styles/global.css->src/styles/globals.css; edited:src/views/SettingsView.tsx; edited:vite.config.ts |
| 32 | `c9eb6c0a2589` | 2026-06-19 | Fix logs pause not freezing content | PRESENT_EXACT | 1 (1/0/0) |  |
| 33 | `e5ccc9573775` | 2026-06-19 | Fix Maximum update depth exceeded in logs under message bursts | PRESENT_EXACT | 3 (3/0/0) |  |
| 34 | `9647eccf30f6` | 2026-06-19 | Show raw error in stream banner without action prefix | PRESENT_WITH_FORK_EDITS | 8 (7/1/0) | edited:src/app/translations.ts |
| 35 | `ff5d364b0f78` | 2026-06-19 | Fix Safari blank flicker when entering logs view | PRESENT_EXACT | 2 (2/0/0) |  |
| 36 | `babaf3a9e474` | 2026-07-14 | Update dashboard icon | PRESENT_EXACT | 4 (4/0/0) |  |
| 37 | `75fca1058600` | 2026-07-17 | Add desktop files | PRESENT_WITH_FORK_EDITS | 32 (26/6/0) | edited:src/App.tsx; edited:src/app/desktop.ts; edited:src/app/translations.ts; edited:src/views/ProfileViews.module.css; edited:src/views/ProfileViews.tsx |
| 38 | `d447d5faccb2` | 2026-07-17 | Fix desktop-host gating and web regressions | PRESENT_WITH_FORK_EDITS | 7 (4/3/0) | edited:src/App.tsx; edited:src/app/desktop.ts; edited:src/views/SettingsView.tsx |
| 39 | `1c6bce8f7408` | 2026-07-17 | Remove sponsors page | PRESENT_WITH_FORK_EDITS | 4 (1/3/0) | edited:src/App.tsx; edited:src/app/translations.ts; edited:src/views/SettingsView.tsx |
| 40 | `c4ed7a3bbc69` | 2026-07-17 | Remove sing-box submodule | PRESENT_RENAMED_OR_SUPERSEDED | 5 (3/1/1) | edited:proto/daemon/started_service.proto; removed-intentional:vendor/sing-box |
| 41 | `a742fa013578` | 2026-07-17 | Improve desktop profile interactions | PRESENT_WITH_FORK_EDITS | 13 (10/3/0) | edited:src/app/translations.ts; edited:src/views/ProfileViews.module.css; edited:src/views/ProfileViews.tsx |
| 42 | `a4be606198c0` | 2026-07-17 | Add standalone tools and diagnostic reports | PRESENT_WITH_FORK_EDITS | 8 (5/3/0) | edited:src/App.tsx; edited:src/app/desktop.ts; edited:src/app/translations.ts |
| 43 | `fff2bc0742ee` | 2026-07-17 | Add tray menu and polish desktop chrome | PRESENT_WITH_FORK_EDITS | 11 (8/3/0) | edited:src/App.tsx; edited:src/app/desktop.ts; edited:src/app/translations.ts |
| 44 | `d8e174b74431` | 2026-07-17 | Drop the app version and speed rows and restyle the settings actions | PRESENT_WITH_FORK_EDITS | 3 (1/2/0) | edited:src/app/translations.ts; edited:src/views/SettingsView.tsx |
| 45 | `cd2801a1939b` | 2026-07-17 | Finish aligning desktop setup and settings | PRESENT_WITH_FORK_EDITS | 5 (2/3/0) | edited:src/App.tsx; edited:src/app/desktop.ts; edited:src/app/translations.ts |
| 46 | `8aaf3ad1cb5f` | 2026-07-17 | Fix connect flicker | PRESENT_EXACT | 2 (2/0/0) |  |
| 47 | `b91ff27a4175` | 2026-07-17 | Persist desktop servers through the host | PRESENT_WITH_FORK_EDITS | 2 (0/2/0) | edited:src/App.tsx; edited:src/app/desktop.ts |
| 48 | `11a6dd7dfb33` | 2026-07-17 | Handle desktop service ownership and package repair | PRESENT_WITH_FORK_EDITS | 3 (1/2/0) | edited:src/app/desktop.ts; edited:src/app/translations.ts |
| 49 | `74cfb5146ce2` | 2026-07-17 | Align core settings with the packaged daemon | PRESENT_WITH_FORK_EDITS | 2 (0/2/0) | edited:src/app/desktop.ts; edited:src/views/SettingsView.tsx |
| 50 | `6237c9625db8` | 2026-07-17 | Show a single desktop startup spinner | PRESENT_WITH_FORK_EDITS | 2 (1/1/0) | edited:src/App.tsx |
| 51 | `955a86c92543` | 2026-07-17 | Render tray groups as a stable three-level cascade | PRESENT_EXACT | 2 (2/0/0) |  |
| 52 | `8dd55f14efb7` | 2026-07-17 | Prefer Apple emoji with a bundled Noto fallback | PRESENT_WITH_FORK_EDITS | 5 (4/1/0) | edited:vite.config.ts |
| 53 | `ce584c0331e2` | 2026-07-17 | Persist desktop preferences through the host | PRESENT_WITH_FORK_EDITS | 11 (9/2/0) | edited:src/App.tsx; edited:src/app/desktop.ts |
| 54 | `774fa128e453` | 2026-07-17 | Animate tray menu content on entry | PRESENT_EXACT | 1 (1/0/0) |  |
| 55 | `8879a487ee80` | 2026-07-17 | Keep desktop streams synchronized while hidden | PRESENT_WITH_FORK_EDITS | 6 (4/2/0) | edited:src/App.tsx; edited:src/api/daemon.ts |
| 56 | `1c93c17812f8` | 2026-07-17 | Keep the service toggle background while busy | PRESENT_EXACT | 1 (1/0/0) |  |
| 57 | `6c64362f4731` | 2026-07-17 | Disable the tray switch while transitioning | PRESENT_EXACT | 2 (2/0/0) |  |
| 58 | `06602faebd85` | 2026-07-17 | Fix desktop spinner color | PRESENT_EXACT | 1 (1/0/0) |  |
| 59 | `435542df2238` | 2026-07-17 | Cleanup comments | PRESENT_WITH_FORK_EDITS | 32 (26/6/0) | edited:.github/workflows/ci.yml; edited:.github/workflows/deploy.yml; edited:index.html; edited:src/App.tsx; edited:src/views/ProfileViews.tsx |
| 60 | `6dbf60f6273d` | 2026-07-17 | fix: resolve react doctor diagnostics | PRESENT_WITH_FORK_EDITS | 46 (42/4/0) | edited:src/App.tsx; edited:src/app/desktop.ts; edited:src/views/ProfileViews.tsx; edited:src/views/SettingsView.tsx |
| 61 | `4a28606ccc5f` | 2026-07-17 | Update dependencies | PRESENT_EXACT | 2 (2/0/0) |  |
| 62 | `dfa351777360` | 2026-07-17 | Add settings for Tray | PRESENT_WITH_FORK_EDITS | 3 (0/3/0) | edited:src/app/desktop.ts; edited:src/app/translations.ts; edited:src/views/SettingsView.tsx |
| 63 | `3a0ecad5b765` | 2026-07-17 | Cleanup code | PRESENT_WITH_FORK_EDITS | 24 (20/4/0) | edited:src/App.tsx; edited:src/api/daemon.ts; edited:src/views/ProfileViews.tsx; edited:src/views/SettingsView.tsx |
| 64 | `ed4affb2e71c` | 2026-07-17 | Refresh login item state after updates | PRESENT_WITH_FORK_EDITS | 1 (0/1/0) | edited:src/views/SettingsView.tsx |
| 65 | `055d88d046a5` | 2026-07-17 | desktop: Add updater | PRESENT_WITH_FORK_EDITS | 10 (6/4/0) | edited:src/App.tsx; edited:src/app/desktop.ts; edited:src/app/translations.ts; edited:src/views/SettingsView.tsx |
| 66 | `0fb8ff3b0cc8` | 2026-07-17 | Fix service failure reporting | PRESENT_WITH_FORK_EDITS | 4 (3/1/0) | edited:src/App.tsx |
| 67 | `7925a540df46` | 2026-07-17 | desktop: Add share support | PRESENT_WITH_FORK_EDITS | 7 (4/3/0) | edited:src/app/desktop.ts; edited:src/app/translations.ts; edited:src/views/ProfileViews.tsx |
| 68 | `d2f9b6e30785` | 2026-07-17 | desktop: Fix Tailscale | PRESENT_WITH_FORK_EDITS | 14 (12/2/0) | edited:src/api/daemon.ts; edited:src/app/desktop.ts |
| 69 | `ba26bed75b60` | 2026-07-17 | desktop: Add insecure mode | PRESENT_WITH_FORK_EDITS | 4 (1/3/0) | edited:src/app/desktop.ts; edited:src/app/translations.ts; edited:src/views/SettingsView.tsx |
| 70 | `d15a293fe40f` | 2026-07-17 | desktop: Fix update view | PRESENT_EXACT | 2 (2/0/0) |  |
| 71 | `ccc79a764a66` | 2026-07-17 | desktop: Fix missing copy/paste for terminal | PRESENT_WITH_FORK_EDITS | 2 (1/1/0) | edited:src/app/desktop.ts |
| 72 | `79d6f2df972a` | 2026-07-17 | Fix pnpm lockfile configuration | PRESENT_EXACT | 1 (1/0/0) |  |
| 73 | `87e810ae8420` | 2026-07-17 | Add openvpn and openconnect | PRESENT_WITH_FORK_EDITS | 17 (13/4/0) | edited:proto/daemon/started_service.proto; edited:src/App.tsx; edited:src/api/daemon.ts; edited:src/app/translations.ts |
| 74 | `0f950a0b3af4` | 2026-07-18 | Refactor endpoint in started interface | PRESENT_WITH_FORK_EDITS | 16 (12/4/0) | edited:proto/daemon/started_service.proto; edited:src/App.tsx; edited:src/api/daemon.ts; edited:src/app/translations.ts |
| 75 | `926abf4c78c0` | 2026-07-19 | desktop: Fix SSO support for anyconnect | PRESENT_WITH_FORK_EDITS | 4 (1/3/0) | edited:proto/daemon/started_service.proto; edited:src/app/desktop.ts; edited:src/app/translations.ts |
| 76 | `375014aa068c` | 2026-07-19 | desktop: Fix linux toolbar | PRESENT_EXACT | 2 (2/0/0) |  |
| 77 | `b44bea289a65` | 2026-07-19 | desktop: Fix tray | PRESENT_EXACT | 2 (2/0/0) |  |
| 78 | `d251fedb1121` | 2026-07-19 | desktop: Fix log | PRESENT_WITH_FORK_EDITS | 1 (0/1/0) | edited:src/api/daemon.ts |
| 79 | `66115761ecfc` | 2026-07-21 | desktop: Improve profile editor | PRESENT_WITH_FORK_EDITS | 5 (1/4/0) | edited:src/App.tsx; edited:src/app/desktop.ts; edited:src/views/ProfileViews.module.css; edited:src/views/ProfileViews.tsx |
| 80 | `311b03180509` | 2026-07-23 | desktop: Fix Fortinet SSO again | PRESENT_WITH_FORK_EDITS | 3 (1/2/0) | edited:proto/daemon/started_service.proto; edited:src/app/desktop.ts |
| 81 | `1f7906c485fc` | 2026-07-24 | desktop: Add JSON completion | PRESENT_WITH_FORK_EDITS | 7 (5/2/0) | edited:src/app/desktop.ts; edited:src/views/ProfileViews.tsx |
| 82 | `f2e702cfd798` | 2026-07-25 | Fix urltest | PRESENT_WITH_FORK_EDITS | 5 (3/2/0) | edited:src/api/daemon.ts; edited:src/views/SettingsView.tsx |
| 83 | `5a8c3aeafb56` | 2026-07-31 | Fix hover affordance mismatches | PRESENT_WITH_FORK_EDITS | 6 (5/1/0) | edited:src/views/ProfileViews.module.css |
| 84 | `7021b7332b2b` | 2026-08-03 | Fix Tailscale SSH auth banner loss | PRESENT_EXACT | 2 (2/0/0) |  |
| 85 | `9242620c8395` | 2026-08-12 | Improve groups | PRESENT_EXACT | 2 (2/0/0) |  |
| 86 | `e5b293de743b` | 2026-08-12 | desktop: Fix terminal layout | PRESENT_EXACT | 5 (5/0/0) |  |
| 87 | `4f29dcc7b932` | 2026-08-15 | Add Taildrop support | PRESENT_WITH_FORK_EDITS | 21 (17/4/0) | edited:proto/daemon/started_service.proto; edited:src/App.tsx; edited:src/app/desktop.ts; edited:src/app/translations.ts |
| 88 | `96b91aab307d` | 2026-08-15 | Fix menu dismissed by unrelated scroll | PRESENT_EXACT | 1 (1/0/0) |  |
| 89 | `b656fe013820` | 2026-08-23 | desktop: Add power report | PRESENT_WITH_FORK_EDITS | 11 (8/3/0) | edited:src/App.tsx; edited:src/app/desktop.ts; edited:src/app/translations.ts |
| 90 | `564dd76b2382` | 2026-08-23 | Update dependencies | PRESENT_EXACT | 2 (2/0/0) |  |
| 91 | `c4c39ac92c74` | 2026-09-03 | desktop: Always show the update track selector | PRESENT_WITH_FORK_EDITS | 2 (0/2/0) | edited:src/app/desktop.ts; edited:src/views/SettingsView.tsx |
| 92 | `9c498355c997` | 2026-09-19 | Fix dashboard cards pairing traffic charts with compact cards | PRESENT_EXACT | 2 (2/0/0) |  |
| 93 | `a9b1dd7595cc` | 2026-09-28 | Improve connections empty state | PRESENT_WITH_FORK_EDITS | 2 (1/1/0) | edited:src/app/translations.ts |
| 94 | `f231354bc786` | 2026-09-28 | desktop: Remove remote control retries | PRESENT_WITH_FORK_EDITS | 1 (0/1/0) | edited:src/app/desktop.ts |
| 95 | `3e4bf08e5bf5` | 2026-10-11 | desktop: Fix tray submenu alignment under top panels | PRESENT_EXACT | 1 (1/0/0) |  |

## 22 个最终树差异文件

| 文件 | 类型 | 解释 |
|---|---|---|
| `.github/workflows/ci.yml` | M | fork 的 desktop shell materialise step |
| `.github/workflows/deploy.yml` | M | fork 的 Pages/gh-pages 发布配置 |
| `index.html` | M | JieJiebox 品牌标题 |
| `proto/daemon/started_service.proto` | M | fork 增加的配置预览/通知/证书服务 |
| `src/App.tsx` | M | JieJiebox 品牌与 fork 页面 |
| `src/api/daemon.configuration.test.ts` | A | fork-only 配置 API 测试 |
| `src/api/daemon.ts` | M | fork 配置 JSON API 与 BOM |
| `src/app/configStatus.plan.test.ts` | A | fork-only 配置计划测试 |
| `src/app/configStatus.test.ts` | A | fork-only 配置状态测试 |
| `src/app/configStatus.ts` | A | fork-only 配置状态/预览实现 |
| `src/app/desktop.ts` | M | fork 的 updateRemote 返回类型/预览 API |
| `src/app/remoteUpdate.test.ts` | A | fork-only 远端更新测试 |
| `src/app/remoteUpdate.ts` | A | fork-only 远端更新实现 |
| `src/app/subscriptionQuota.test.ts` | A | fork-only 订阅配额测试 |
| `src/app/subscriptionQuota.ts` | A | fork-only 订阅配额实现 |
| `src/app/translations.ts` | M | fork 额外 4 个订阅配额 key + 中文 |
| `src/lib/subscription.test.ts` | A | fork-only 订阅解析测试 |
| `src/views/ProfileViews.module.css` | M | fork 配额/配置状态样式 |
| `src/views/ProfileViews.tsx` | M | fork 配额/配置状态/远端更新组件 |
| `src/views/SettingsView.tsx` | M | fork 的 JieJiebox Source/Releases 链接 |
| `tsconfig.json` | M | fork @shared include |
| `vite.config.ts` | M | fork @shared alias/manifest 品牌 |

## CSS lint 基线

`pnpm lint:css` 仍报 4 个 fork-only 规则错误：`src/views/ProfileViews.module.css` 292 `font-weight:600`、299/314 `gap:2px`、335 `margin-top:2px`。这些规则是 fork 订阅/配置状态样式，上游没有对应规则；值和 600 属于既有设计值。修复需要改 `--weight-semibold`（560）或新增 token，超出本轮“不为绿灯改设计值”的边界，登记为 BASELINE_CONFIRMED，文件/行号和复现命令在 `05_MAIN_KERNEL`/本文件。

## 复核命令

```sh
cd _sync-dashboard
pnpm test && pnpm build
pnpm lint:css   # 预期 4 个 baseline 错误
```
