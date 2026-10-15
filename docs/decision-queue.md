# Decision Queue

Items requiring human review, curated by AI agent sessions and the
automated tooling patrol (see hironow/dotfiles routine). Append new
entries under a dated section; tick the checkbox (or delete the entry)
once the human has decided.

Entry format:

```markdown
## YYYY-MM-DD

- [ ] **<topic>**: <decision needed> — background / options / recommendation
```

---

## 2026-07-11

- [ ] **iac-test-github-provider**: Audit found 3 of 4 tofu test files missing `mock_provider "github" {}`, silently skipping GitHub provider init during `tofu test`. Fixed in branch test/tf-coverage. No further action needed unless you want a CI lint rule to enforce mock_provider declarations for every declared provider.

---

## Open Items

(none yet)
