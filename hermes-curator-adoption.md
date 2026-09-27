---
name: hermes-curator-adoption
description: "Guide for adopting user-owned skills into the curator-managed skill library"
version: 1.0.0
---

# Hermes Curator Adoption Guide

## Why Adopt a Skill?

Skills that are **user-owned** (created_by=None) cannot be edited by the autonomous curator. To enable automatic skill improvements across sessions, adopt the skill into the curator-managed library.

## Adoption Command

```bash
hermes curator adopt <skill-name>
```

Example:
```bash
hermes curator adopt ai-router-gateway-development
```

## What Happens After Adoption?

1. Skill becomes curator-managed
2. Autonomous agents can patch and improve the skill
3. Knowledge accumulates across sessions
4. Future agents benefit from learned patterns and pitfalls

## Current Skills Needing Adoption

Based on recent session activity:

| Skill | Reason |
|-------|--------|
| `ai-router-gateway-development` | Contains project-specific patterns learned this session (RTK compression, Windows deployment fixes, etc.) |

## Alternative: Manual Updates

If you prefer not to adopt, you can manually update the skill file:
- Location: `C:\Users\hedou\AppData\Local\hermes\skills\software-development\<skill-name>\SKILL.md`
- Add sections for new patterns learned

## Supported Operations (Post-Adoption)

Once adopted, the curator can:
- Patch SKILL.md with new patterns
- Add reference documentation
- Create templates and scripts
- Update pitfalls and best practices
