---
name: session-catalogue-close
description: Use when the user asks to conclude, wrap up, or close out the current session for cataloguing purposes (e.g. "conclude this session", "wrap this up", "close out for the catalogue", "this session is now concluded"). Emits a machine-readable marker plus a short away-summary so the session-cataloguing tool can tell this session was cleanly concluded rather than interrupted.
---

# Session Catalogue Close

When invoked, this is the last thing you do in the conversation. Do not
perform any further tool calls or other actions after emitting the
marker below.

1. Write 2-4 sentences summarizing what happened this session and where
   things were left off (an "away-summary" -- useful to someone resuming
   this session later without re-reading the whole transcript).
2. Emit that summary as your final message, wrapped EXACTLY in this
   marker. The literal string `cc-catalogue:session-concluded` is
   load-bearing: a cataloguing tool scans raw session logs for this
   exact substring to detect a clean conclusion. Do not paraphrase or
   reformat it.

<!-- cc-catalogue:session-concluded
away-summary: |
  <your 2-4 sentence summary here, indented two spaces per line>
-->

Do not add anything after this block.
