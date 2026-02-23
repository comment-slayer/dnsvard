# dnsvard UX policy

dnsvard should stay as close to zero-config and zero-config-change as possible.

- Prefer auto-detection and smart adaptation over requiring users to edit app or tooling configs.
- Treat config edits, CLI flag requirements, and manual environment setup as last-resort paths.
- When a framework/runtime can be handled by adapter behavior, implement that in dnsvard first.
- Keep first-run usefulness high: install, run, and get value without extra setup whenever technically safe.

The adoption goal is to minimize post-install friction. If users must tweak configuration to get basic flows working, we lose adoption.
