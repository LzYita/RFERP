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

Release notes are assembled by `changelog.ps1` (called from `release.bat`) from three parts:

1. hand-written bilingual highlights — `release-notes.txt`
2. pull requests, grouped by label via `.github/release.yml`
3. commits pushed straight to `main` (mainline, non-merge)

Add `[skip changelog]` to a commit subject to keep that commit out of the list.

### Language

- Issues and pull request discussion: **English**
- README and release notes: **English + Chinese**

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

发行说明由 `changelog.ps1`（由 `release.bat` 调用）组合三部分：

1. 手写双语要点 —— `release-notes.txt`
2. 按标签分组的 PR 列表（规则见 `.github/release.yml`）
3. 直推 `main` 的提交（主干上的非合并提交）

提交信息里带 `[skip changelog]` 可让该提交不进入列表。

### 语言

- Issue 与 PR 讨论：**英文**
- README 与发行说明：**中英双语**
