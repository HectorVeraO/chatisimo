# Issue Specification Format & Guidelines

Canonical structure, separation of concerns, and worked reference for feature issues in Chatisimo, based on the Universidad Católica story (#105).

---

## 1. Core Principle: Functional vs. Technical Separation

An issue serves both product/QA stakeholders and implementers. To maintain clarity:
- **Functional content** (`Goal`, `Acceptance criteria`, and `Examples`) describes strictly what the user/operator experiences and observes. It contains **no code package names, no DB table/migration names, no internal struct symbols**.
- **Technical detail** (`Technical Implementation Notes`) is placed inside an expandable `<details>` section at the bottom, defining architecture, packages, database schemas, gateway plumbing, and verification strategy.

---

## 2. Standard Issue Structure

```markdown
### Goal

<Concise, 1-3 sentence summary of the feature, user capability, or business value.>

### Acceptance criteria

- [ ] **<Criterion 1 Title>:** <Observable behavioral condition. Inputs, triggers, outputs, or error states visible to users or operators.>
- [ ] **<Criterion 2 Title>:** <Rules, calculations, constraints, or SLA requirements without citing internal code symbols.>
- [ ] **<Criterion 3 Title>:** <Command interactions, parameters, permissions, or notifications.>

### Notes

- Operational parameter names registered in settings or guild configuration.
- External dependencies or Discord portal permissions (e.g. Gateway intents).
- Test constraints: hermetic fakes, zero live external API calls, native Go fuzzing, synthetic test data only.

<details>
<summary><b>📋 Examples per Acceptance Criterion (Click to expand)</b></summary>

**AC 1: <Scenario Title>**
- <Input / Trigger>: `<command or message>`
- <Bot Response / Visual Output>:
  > <Quoted markdown mockup of actual Discord reply, embed, or alert>

**AC 2: <Scenario Title>**
...

</details>

<details>
<summary><b>⚙️ Technical Implementation Notes (Click to expand)</b></summary>

- **Zero-New-Dependency Stack:** Packages used from stdlib and existing `go.mod`.
- **Module & Schema Isolation (`internal/<name>/`):**
  - Public API in `internal/<name>/` (`Service`, domain types, errors).
  - Internal storage under `internal/<name>/internal/store/` with Goose migrations and `sqlc` queries in dedicated Postgres schema `<name>`.
- **Discord Gateway & Router Adapter (`internal/discord/`):**
  - Gateway intents required in `internal/discord/discord.go`.
  - Interaction command trees (`disgo/discord` builders) and event listeners.
- **Algorithms, State & Caching:**
  - In-memory TTL caches, background goroutines, debounce/mutex locks, or heuristics.
- **LLM Client Integration (when applicable):**
  - Prompt configuration in `platform.setting`, output bounds, and deterministic local template fallbacks.
- **Verification & Test Strategy:**
  - Fuzz tests, fake Discord Gateway event mocks, and fake HTTP servers.

</details>
```

---

## 3. Checklist for Issue Authors

1. **Acceptance criteria test**: could a non-engineer verify every bullet point in `Acceptance criteria` using only Discord and slash commands? If a bullet requires reading Go code or inspecting SQL schemas, move that detail to `Technical Implementation Notes`.
2. **Visual mockups**: provide concrete chat message examples in `Examples per Acceptance Criterion` to lock down formatting, emoji branding, and relative timestamps (`<t:UNIX:R>`).
3. **Synthetic data only**: use invented IDs (`123456789012345678`) and placeholder names; never paste real Discord user IDs, email addresses, or guild secrets.
