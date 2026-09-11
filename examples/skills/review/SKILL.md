---
name: review
description: Review a code change for correctness, regressions, and missing edge cases.
---

# Review a change

Read the diff and relevant surrounding code. Trace changed values through their
callers. Look for concrete failure cases and missing coverage.

Report findings by severity with file references and a short explanation of how
to reproduce the problem. If you find none, say so and identify any untested areas.
