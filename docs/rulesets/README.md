# Rulesets / 规则集

Version-controlled copies of the repository rulesets that apply to `main`.
GitHub does **not** read this folder — it is an archive, so the configuration can be reviewed, diffed and re-applied.

这里保存作用于 `main` 的规则集定义副本。**GitHub 不会读取此目录** —— 它只是存档，便于评审、对比与重建。

| File | Ruleset | Purpose |
|---|---|---|
| `main-safety.json` | `main-safety` | no force pushes, no branch deletion / 禁止强推与删除分支 |
| `main-flow.json` | `main-flow` | pull requests + `Windows / Go verify` before merge / 合并前须走 PR 并通过 CI |

## Apply / 应用

```bash
gh api -X POST repos/LzYita/RFERP/rulesets --input docs/rulesets/main-safety.json
gh api -X POST repos/LzYita/RFERP/rulesets --input docs/rulesets/main-flow.json
```

## Inspect / 查看

```bash
gh api repos/LzYita/RFERP/rulesets
gh api repos/LzYita/RFERP/rules/branches/main
```

## Notes / 说明

- `main-flow` lists the repository owner in `bypass_actors`, so direct pushes for trivia stay possible; everyone else must go through a pull request.
- `allowed_merge_methods` is limited to `merge`, matching the project convention (no squash, no rebase).
- Update a ruleset with `PUT /repos/{owner}/{repo}/rulesets/{id}` and keep these files in sync.
