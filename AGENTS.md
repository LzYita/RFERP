# AGENTS.md — 协作代理的工作约定

本文件是参与本仓库的自动化代理（以及人类协作者）的行为约定。
仓库自身的规则见 `CONTRIBUTING.md`、`docs/plans/wails-migration.md`、`docs/rulesets/`。

## 合并与推送：一律等明确指示

- **不要自行合并 PR。** 实现 → 测试 → 开 PR → 把 CI 跑绿，到此为止；
  **合并必须等仓库所有者明确说「可以合并」**（2026-10-14 明确要求）。
- 同样的限制适用于：强推任何分支、改 `main` 的 ruleset、删远端分支/tag、改仓库设置。
- **不要直接向 `main` 推送**（ruleset 也禁强推），所有改动走 PR。
  曾发生过「提交并推送」被理解成直接推 `main` 的事故，最后只能靠 Revert 收场，
  还在 `main` 上留下了一条噪音提交。
- 推送前把与 CI 等价的检查跑一遍（见下）。

## 提交

- conventional commits，小写英文前缀：`feat(roundN): ...`、`fix(scope): ...`、`docs: ...`。
- 正文写**为什么**——尤其是「看起来可以更简单、但没那么做」的地方。
  本仓库的沟通习惯是把权衡与踩过的坑写进提交信息与注释，而不是只描述改动内容。
- 注释解释 *why* 与实测依据；平台差异一律 `<name>_windows.go` / `<name>_other.go` 配对。
- 中文 UI 硬编码是当前正确状态；i18n 留到 Round 10，**不要**提前局部国际化。

## 推送前的检查（与 CI 等价）

```
gofmt -l .          # 必须无输出
git diff --check    # 空白检查
go vet ./...
go build ./...
go test ./...
cd web; npm test; npm run build      # build 含 tsc -b；另跑 npm run lint
node scripts/browser-gate.mjs        # 需要 Chromium；本机可设 PW_CHROME 指向 chrome.exe
```

`browser-gate.mjs` 只能在本地跑（CI 里没有浏览器）。它有两种形态：默认从源码起后端，
`GATE_EXE` 指向现成产物时走产物形态——**发版前必须用后者再跑一遍**，
「源码能跑」和「发出去的那个 exe 能跑」是两件事。

## 本机环境（仅限 LzYita 的开发机，换机器时删掉本节）

- `github.com` 的 DNS 被劫持，**git HTTPS 不可用**；`api.github.com` 可达。
  因此推送走 `gh` 的 Git Data API（blobs → trees → commits → PATCH ref）。
- 取远端对象可走仓库自带的加速器：
  `git fetch https://ghfast.top/https://github.com/LzYita/RFERP.git <ref>`。
- **`core.autocrlf=true`，而仓库里所有 blob 都是 LF。** 用 API/脚本上传文件前
  必须先做 CRLF → LF，否则整份文件会显示成全量改动，`git diff --check` 也会失败
  （这一条已经踩过：4 个文件被按 CRLF 推上去过）。
- PowerShell 5.1：`&&` 不可用，用 `;` 或 `if ($?)`；命令行里的 `|`、`"`、`%`
  极易被吞，复杂逻辑改用 `node -e` 或写成临时 `.ps1` 文件再执行。
