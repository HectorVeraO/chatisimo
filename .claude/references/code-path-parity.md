# Code Path Parity: full matrix

Passive reference, reached from root `AGENTS.md` "Code Path Parity". It holds the full cross-subproject parity rows; root keeps one summary bullet per group naming its files. Read the full row here before touching any file named in root's Code Path Parity section.

Each row states:

- **Files**: every copy or twin, by repo-relative path. No "and similar": a file not listed is not checked.
- **What duplicates**: the logic that is shared and the shape the duplication has (byte-identical copy, matching halves of a contract, same literal across layers).
- **Check**: for byte-identical copies, the command that recomputes the hash of every copy and the expected value; a hash nobody can recompute is not a baseline. For copies that have already diverged, say so and name how to establish the good version before propagating anything.
- **What breaks silently**: the failure a missed update produces, so the reader knows what to look for.

Format and examples: `.claude/references/code-path-parity-format.md`.

---

<!-- Add parity rows below this line. The skill never edits below. -->
