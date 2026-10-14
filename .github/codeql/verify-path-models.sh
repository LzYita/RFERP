#!/usr/bin/env bash
set -euo pipefail

# Analyze throwaway probes without PR diff filtering or uploading deliberate findings.
work=$(mktemp -d "$RUNNER_TEMP/rferp-path-models.XXXXXX")
src="$work/source"
mkdir -p "$src/internal/paths" "$src/probe" "$work/models"
cp internal/paths/paths.go internal/paths/path_other.go "$src/internal/paths/"
printf 'module app\n\ngo 1.26.5\n' > "$src/go.mod"
cat > "$src/probe/main.go" <<'GO'
package main
import (
  "net/http"
  "os"
  "path/filepath"
  "app/internal/paths"
)
func raw(r *http.Request) {
  _, _ = os.Stat(r.FormValue("path")) // expect-raw
}
func child(r *http.Request) {
  _, _ = os.Stat(filepath.Join(paths.DataDir(), r.FormValue("name"))) // expect-child
}
func checked(r *http.Request) {
  target, err := paths.ResolveExistingUnder("/trusted-root", r.FormValue("path"))
  if err != nil { return }
  _, _ = os.Stat(target) // expect-checked
}
func main() {}
GO
cat > "$work/models/qlpack.yml" <<'YAML'
name: rferp/path-model-sentinels
version: 0.0.0
library: true
extensionTargets:
  codeql/go-all: '*'
dataExtensions:
  - '*.model.yml'
YAML
cp .github/codeql/extensions/rferp-paths.model.yml "$work/models/"
(cd "$src" && "$CODEQL_BIN" database create "$work/db" --language=go --source-root="$src" --command='go build ./...')
query='codeql/go-queries:Security/CWE-022/TaintedPath.ql'
"$CODEQL_BIN" database analyze "$work/db" "$query" --format=sarif-latest --output="$work/baseline.sarif"
"$CODEQL_BIN" database analyze "$work/db" "$query" --format=sarif-latest --output="$work/modeled.sarif" \
  --additional-packs="$work/models" --model-packs=rferp/path-model-sentinels
python3 - "$work" <<'PY'
import json, pathlib, sys
work = pathlib.Path(sys.argv[1])
lines = (work / "source/probe/main.go").read_text().splitlines()
expected = {tag: i for i, line in enumerate(lines, 1)
            for tag in ("raw", "child", "checked") if "expect-" + tag in line}
def findings(name):
    runs = json.loads((work / name).read_text())["runs"]
    result = set()
    for run in runs:
        for finding in run.get("results", []):
            if finding["ruleId"] != "go/path-injection":
                continue
            loc = finding["locations"][0]["physicalLocation"]
            if loc["artifactLocation"]["uri"].endswith("probe/main.go"):
                result.add(loc["region"]["startLine"])
    return result
baseline, modeled = findings("baseline.sarif"), findings("modeled.sarif")
assert baseline == set(expected.values()), ("baseline", baseline, expected)
assert modeled == {expected["raw"], expected["child"]}, ("modeled", modeled, expected)
print("Path models verified: unchecked paths/child names remain findings; physical targets do not.")
PY
