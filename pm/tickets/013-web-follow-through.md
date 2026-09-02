---
id: 013
title: Web follow-through after 007 (DESIGN §2.3 persistence, §2.5 asks)
kind: normal
size: 1
status: declined
updated: 2026-09-02
release: demo-1
---

# 013 — Web follow-through (after 007 lands)

Binding, from `docs/DESIGN.md` §6 ticket 3: fold the loading step states into `connecting.step` if
007 landed them as states; persistence rules (§2.3: user turn saved before I/O, 2 s checkpoint of the
streaming reply, `streaming → interrupted` on load); one `/me` path (the post-request refresh and the
60 s poll dispatch into the same reducer). Evidence: W1–W7 as vitests over the reducers; a screenshot
of the reload case. Concept budget 0. Dispatched when 007 lands and the PM has read its report.

## Log

## Report


## Ruling (PM, 2026-09-02 14:50)

Folded into ticket 014 (promise 4). Not dispatched separately.
