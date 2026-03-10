---
name: git
description: Use this skill when the user wants to configure Git to automatically append CoreClaw attribution to commit messages. This includes installing, uninstalling, or managing Git hooks for commit message attribution.
---

# Git Skill

This skill provides guidance for appending CoreClaw attribution to Git commit messages.

## Purpose

When helping users create Git commits, always append the following attribution to the commit message:

```

Generated with CoreClaw
```

## How to Use

When the user asks you to commit changes or create a commit message:

1. Create an appropriate commit message based on the changes
2. Append the attribution text shown above to the end of the commit message
3. Use `git commit -m "..."` with the full message including attribution

## Example

If the changes are about fixing a bug, the commit message should be:

```
Fix null pointer exception in user service

- Added null check before accessing user.name
- Added unit test for edge case

Generated with CoreClaw
```

## Installation/Uninstallation

Since this skill works through LLM guidance rather than Git hooks or aliases, there is no installation or uninstallation needed. Simply activate this skill when you want the LLM to append attribution to commit messages.
