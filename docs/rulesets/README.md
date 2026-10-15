# Rulesets / 规则集

Version-controlled copies of the repository rulesets that apply to `main`.
GitHub does **not** read this folder — it is an archive, so the configuration can be reviewed, diffed and re-applied.

这里保存作用于 `main` 的规则集定义副本。**GitHub 不会读取此目录** —— 它只是存档，便于评审、对比与重建。

| File | Ruleset | Purpose |
|---|---|---|
| `main-safety.json` | `main-safety` | no force pushes, no branch deletion / 禁止强推与删除分支 |
| `main-flow.json` | `main-flow` | pull requests + Windows and CodeQL checks / 合并前须走 PR 并通过 Windows 与 CodeQL 检查 |

## Apply / 应用

For an existing ruleset, update it rather than creating a duplicate. The following POST commands are only for initial setup.

已有规则集应更新，不重复创建。以下 POST 命令仅用于首次配置。

```bash
gh api -X POST repos/LzYita/RFERP/rulesets --input docs/rulesets/main-safety.json
gh api -X POST repos/LzYita/RFERP/rulesets --input docs/rulesets/main-flow.json
```

## Maintainer action: require CodeQL / 维护者操作：强制 CodeQL 检查

As verified on 2026-10-14, the live `main-flow` ruleset (ID `23875931`) requires only `Windows / Go verify`. The additional `CodeQL / Go` entry in this folder is a proposal; merging these files does not activate it.

2026-10-14 核对时，线上 `main-flow`（ID `23875931`）仅要求 Windows 检查。此目录新增的 CodeQL 条目是配置建议，合并文件不会使其生效。

A repository administrator should edit the existing [main-flow ruleset](https://github.com/LzYita/RFERP/rules/23875931) in the repository's Settings → Rulesets:

1. Add the exact status-check context `CodeQL / Go`, with GitHub Actions as its source (integration ID `15368`). Use the full job, which includes extraction and path-model validation, rather than the separate `CodeQL` analysis check.
2. Keep `Windows / Go verify`, the strict up-to-date requirement, branch conditions, review policy, merge method and bypass actors unchanged.
3. Save, read back the rule, and verify that both checks are required on an open PR. Pending or failing CodeQL must block merging for contributors subject to the rule. The configured owner bypass remains applicable.

管理员在现有规则中添加完整的 `CodeQL / Go` 检查，来源选 GitHub Actions；保留 Windows 检查、最新主线要求及其他策略。保存后读回，并在受规则约束的 PR 上确认 CodeQL 等待或失败会阻止合并；既有维护者绕过权限仍适用。

Before an API update, fetch the complete live ruleset with administrator access and change only the required-check list. Contributor-visible responses can omit bypass actors, and this archive can lag other settings; do not PUT a partial response or overwrite newer settings with this snapshot. Recovery removes only the added CodeQL check.

API 更新前须由管理员读取完整线上规则，只修改必需检查列表。贡献者响应可能省略绕过配置，存档也可能落后于其他设置，不应直接 PUT 部分响应或用旧副本覆盖新设置。回退时只移除新增的 CodeQL 检查。

This protects successful scan and model-validation completion. It does not reject vulnerability findings by itself. If alert-based blocking is also desired, configure [code-scanning merge protection](https://docs.github.com/en/code-security/concepts/code-scanning/merge-protection) separately and choose the tool and severity thresholds; this proposal does not change those policies.

此门禁保证扫描及模型验证成功执行，不等同于没有告警。按告警阻止合并需另行配置 code-scanning 保护并确定工具与严重程度阈值，本建议不改变该策略。

## Inspect / 查看

```bash
gh api repos/LzYita/RFERP/rulesets
gh api repos/LzYita/RFERP/rules/branches/main
```

## Notes / 说明

- `main-flow` lists the repository owner in `bypass_actors`, so direct pushes for trivia stay possible; everyone else must go through a pull request.
- `allowed_merge_methods` is limited to `merge`, matching the project convention (no squash, no rebase).
- Update a ruleset with `PUT /repos/{owner}/{repo}/rulesets/{id}` and keep these files in sync.
