# fibe-distilled Alloy Models

These models are executable notes for the fibe-distilled control-plane slice. They are intentionally smaller than Fibe's Rails models and cover only scoped fibe-distilled behavior.

Run all commands when Alloy is installed:

```sh
alloy exec -q -f -t json -o /tmp/fibe-distilled-alloy -c '*' docs/alloy/fibe_distilled_lifecycle.als
```

`./bin/check` runs `bin/linters/alloy-analyze`; the linter skips cleanly when Alloy is not installed.

Expected results:

- `run` commands are SAT.
- `check` commands are UNSAT.
