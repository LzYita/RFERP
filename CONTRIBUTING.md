# Contributing / 参与开发

**English** | **中文**

---

## English

### Branching and commits

- Branch off `main`: `feat/...`, `fix/...`, `chore/...`, `ci/...`
- Use [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:`, `chore:`, `ci:`
- Keep history traceable — pull requests are merged with a **merge commit** (no squash, no rebase)

### Workflow: pull requests first

Open a pull request for every change that should appear in the release notes — features, fixes, refactors, dependency bumps, CI and migrations. Even when you merge your own PR, the flow is the same:

1. `git switch -c fix/short-name main`
2. commit, then `git push -u origin fix/short-name`
3. open a PR and add a label — the label decides the release-notes category
4. wait for **`Windows / Go verify`** to pass, then merge with a merge commit

**Direct pushes to `main` are reserved for trivia** — typos, comments, `.gitignore`, README wording, screenshots. Those are not listed automatically, so mention anything user-visible in `release-notes.txt`.

### Release notes

Release notes are assembled from three parts:

1. hand-written bilingual highlights — `release-notes.txt`
2. pull requests, grouped by label via `.github/release.yml`
3. commits pushed straight to `main` (mainline, non-merge)

Add `[skip changelog]` to a commit subject to keep that commit out of the list.

### Release process

The release tooling (build, sign, installer, publish) is **not part of this
repository** and is not published with it. It is maintained privately because it
necessarily contains machine-specific values: where the checkout lives, where the
signing key lives, and which mirror reaches GitHub. None of that belongs in a
public repository.

What is public is the process, and it has not changed:

1. confirm the signing key is present
2. write the hand-written bilingual highlights
3. accumulate the release into the in-app changelog — **before** the build, since
   the changelog ships through `go:embed` and a later write is a no-op
4. build the Wails executable (frontend bundle first, then `wails build` with the
   version injected into `main.version`)
5. package the update zip
6. sign the manifest with the production ed25519 key
7. build the installer
8. publish the installer, the zip and `releases.json` to a GitHub release

Step 3 is the one that has broken before: the in-app changelog page and the
guide header both ship inside the binary, so accumulating after the build leaves
the release one version behind. Keep it before step 4.

Another value has drifted before: `cmd/desktop-wails/wails.json` carries
`info.productVersion`, and Wails writes it into the executable's **file version
resource** (what Explorer's Properties → Details shows). It is independent of
`main.version`, so a build can report the right version in the app and a stale one
in the file properties - exactly what happened before 2.0.0 (the resource said
1.3.0). Update it with the release version before step 4.

A build must never run against a live configuration: `wails build` executes the
application while generating bindings, and the application opens whatever
database its configuration points at. The release tooling therefore builds with
an isolated `APPDATA` and refuses to run if a `config.json` sits where it could be
picked up instead.

### Language

- Issues and pull request discussion: **English**
- README and release notes: **English + Chinese**

### Licensing

This project has **no open-source license; all rights reserved** (see the License
section of the README). Opening an issue is always welcome, and by opening a pull
request you grant the maintainer the rights needed to accept and ship it.

Do not add a LICENSE file, and do not propose vendoring dependencies or relicensing.
If you believe the project should be licensed differently, open an issue first
instead of proposing a change.

---

## 中文

### 分支与提交

- 从 `main` 切分支：`feat/...`、`fix/...`、`chore/...`、`ci/...`
- 使用 [Conventional Commits](https://www.conventionalcommits.org/)：`feat:`、`fix:`、`docs:`、`chore:`、`ci:`
- 保持历史可追溯 —— PR 一律用 **merge commit** 合并（不 squash、不 rebase）

### 流程：优先走 PR

凡是需要出现在发行说明里的改动（功能、修复、重构、依赖升级、CI、迁移），都开 PR。即使自审自合也走同样流程：

1. `git switch -c fix/short-name main`
2. 提交，然后 `git push -u origin fix/short-name`
3. 开 PR 并**打标签** —— 标签决定发行说明的归类
4. 等 **`Windows / Go verify`** 通过后，用 merge commit 合并

**直推 `main` 仅限杂项** —— 错别字、注释、`.gitignore`、README 文案、截图。这些不会被自动列出，若有用户可见的内容请写进 `release-notes.txt`。

### 发行说明

发行说明组合三部分：

1. 手写双语要点 —— `release-notes.txt`
2. 按标签分组的 PR 列表（规则见 `.github/release.yml`）
3. 直推 `main` 的提交（主干上的非合并提交）

提交信息里带 `[skip changelog]` 可让该提交不进入列表。

### 发版流程

发版工具（构建、签名、安装包、发布）**不属于本仓库，也不随本仓库公开**。它是私下维护的，
因为它必然包含本机专属的值：仓库在哪、私钥在哪、以及走哪个镜像访问 GitHub —— 这些都不该
进公共仓库。

公开的是流程本身，流程没有变：

1. 确认签名私钥存在
2. 写手写双语要点
3. 累积本次发版进程序内更新说明 —— **必须在构建之前**，因为更新说明靠 `go:embed` 打进二进制，
   构建之后再累积等于白累积
4. 构建 Wails 可执行文件（先前端打包，再 `wails build` 并把版本注入 `main.version`）
5. 打更新包 zip
6. 用生产 ed25519 私钥签清单
7. 打安装包
8. 把安装包、zip 与 `releases.json` 发布到 GitHub Release

第 3 步是唯一出过故障的一步：程序内「更新说明」页与指南顶部的版本号都编在二进制里，
构建之后累积会让这次发布停在上一版。请保持它在第 4 步之前。

构建绝不能在有真实配置的情况下进行：`wails build` 在生成绑定时会真的运行一次应用，
而应用会去连它配置里指向的数据库。发版工具因此用隔离的 `APPDATA` 构建，
并且一旦发现可能被优先命中的 `config.json` 就直接拒绝执行。

### 语言

- Issue 与 PR 讨论：**英文**
- README 与发行说明：**中英双语**

### 许可证

本项目**未采用开源许可证，保留所有权利**（见 README 的「许可证」一节）。
欢迎提 issue；提交 PR 即表示授予维护者接受并发布该改动所需的权限。

请勿添加 LICENSE 文件，也不要提交「将依赖打包进仓库」或「更换许可证」这类改动。
如果你认为本项目应当采用不同的许可证，请先开 issue 讨论，而不是直接提改动。
