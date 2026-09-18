---
name: hello
description: Greets the user and proves skill loading works.
version: 1.0.0
author: Nimbus-One
triggers: hello, hi, greet
---

# Hello skill

When the user greets you, respond warmly and mention one capability
from the tool list. No scripts needed — the model handles it directly.

```sh
# optional demo script (runs with SKILL_ARG_name)
echo "Hello, ${SKILL_ARG_name:-friend}! Nimbus-One at your service."
```
