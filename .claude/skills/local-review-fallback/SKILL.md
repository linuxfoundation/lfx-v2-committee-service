---
name: local-review-fallback
description: Launch the three local pre-PR reviewers as Claude subagents when the lfx-local-review host reports that Pi is unavailable. A launch table only — it carries no review criteria of its own.
---
<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Local review — Claude fallback

The `lfx-local-review` host has already decided the harness and printed its pins. Launch three reviewers and nothing else. This is a launch table: review criteria, severities, floor rules and KB knowledge stay in the selected skills.

## Launch exactly three generic subagents in one parallel batch

| Role | Registered skill to load |
|---|---|
| `general` | `lfx-general-code-review` |
| `repo_code` | `committee-service-code-reviewer` |
| `repo_learnings` | `committee-service-learnings-reviewer` |

## Tell each subagent which registered skill to load

Those three names are the whole selection mechanism. Tell each subagent to load its named skill and follow it as its entire rulebook, then review the pinned range.

Pass **no** reviewer-skill path. Do not resolve a `SKILL.md` to a physical path, do not parse frontmatter at runtime, do not read a skill file as ordinary text, do not paste or restate its rules into the prompt, and do not accept an ambient substitute. The name is the contract; anything else is a different rulebook wearing the same label.

If a named skill is unavailable, **that role fails loudly and the whole Claude cycle is invalid.** The remedy is to start Claude from the service repo with the `lfx-skills` plugin loaded, so the repo's project skills and the central plugin skills are registered — never to work around skill loading by reading a path. An unregistered skill is a broken session to fix, not an obstacle to route around.

Forbid ambient instruction discovery, but not evidence reads directed by the loaded skill.

Pass unchanged to every subagent: `target repo`, `target_sha`, `base_sha` (or literal `none`), the exact `review exactly:` range, and any `extra` hint. Use the pins from the single harness decision; never rerun the launcher to obtain them.

A subagent error, empty result, or non-review Markdown is a role-labelled all-Claude host failure. Never call it no findings and never synthesize reviewer `INCOMPLETE`. A reviewer-returned first-line `INCOMPLETE — <reason>` passes through. Any failure invalidates the cycle; rerun all three on Claude, never one role and never a mixed harness.
