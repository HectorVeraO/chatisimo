---
title: Privacy Policy
---

# Chatisimo Privacy Policy

**Effective date:** November 1, 2026

This Privacy Policy explains what data the **Chatisimo** Discord application ("the Bot", "we") collects, how that data is used and stored, which external services process it, and how you can delete it. Chatisimo's full source code is publicly auditable at [github.com/HectorVeraO/chatisimo](https://github.com/HectorVeraO/chatisimo).

## 1. Discord Permissions and What We Do Not Collect

Chatisimo connects to Discord with the **`Guilds` (`IntentGuilds`) Gateway intent only**.

- Chatisimo **does not** request the `MessageContent`, `GuildMembers`, or `GuildPresences` privileged intents.
- Chatisimo **never** reads, monitors, or stores regular chat messages sent in your Discord channels or direct messages. It only receives data when a user explicitly invokes one of Chatisimo's slash commands (`/ge`, `/chatisimo`, `/uc`, `/uc-config`) or interacts with a button, select menu, or modal on a Chatisimo message.
- Chatisimo does not collect real names, email addresses, IP addresses, billing data, or tracking cookies.

## 2. Data We Collect and Store

Chatisimo stores operational records in a private PostgreSQL database:

### Discord Identifiers and Transient Metadata

- **Snowflake IDs**: Discord server (guild) IDs, Discord user IDs of users who run gift-exchange commands or are selected as a server's UC-fan member via `/uc-config`, and Discord text-channel IDs selected for proactive UC alerts via `/uc-config`.
- **In-memory cache only**: server names and Discord UI/server locale tags (such as `es-419` or `en-US`) observed from Gateway guild events and interactions are held transiently in process memory to localize replies and label DM autocomplete choices, and are discarded whenever the process restarts.

### Gift Exchange Records (`giftexchange` schema)

- **Exchanges and rules**: exchange name (up to 100 characters), server ID, organizer user ID, lifecycle state (`open`, `drawn`, `revealed`, `cancelled`), optional scheduled draw and reveal timestamps, reminder lead time, dropout policy, `avoid_last_round` flag, and `questions_enabled` flag.
- **Participation, exclusions, and assignments**: participant membership timestamps, organizer-defined draw exclusions, and secret giver-to-recipient pairings per round.
- **Optional gift preferences**: text voluntarily entered by a participant in `/ge preferences` (`likes`, `dislikes`, and `details`, up to 300 characters each).
- **Anonymous questions and answers**: question and answer text submitted through `/ge ask` and answer modals (up to 1,000 characters each, stored after optional style rephrasing) and notification delivery status (`delivered`, `stale`, `undeliverable`).

### UC Match Calendar Records (`uccalendar` schema)

- **Per-server settings**: optional target member user ID (`friend_user_id`), optional alert channel ID (`channel_id`), and optional message language (`es-CL` or `en-US`) configured by a server manager via `/uc-config`.
- **Alert deduplication**: alert keys and timestamps (`sent_alert`) recording that a Friday or matchday alert was already posted to a server so duplicate alerts are not sent.

### Operational Logs

- Server logs record structured operational events (command or job status, latency, and opaque request reference IDs). Discord tokens, user message bodies, snowflake IDs, and secret gift-exchange pairings are excluded from application logs.

## 3. How We Use Data

We use stored data strictly to:

- Run gift-exchange draws, enforce exclusions and `avoid_last_round` constraints, share a recipient's gift preferences with their assigned giver, and relay anonymous questions, answers, reminders, and reveals over Discord.
- Format `/uc` availability reports and deliver Friday and matchday UC match alerts to servers that configured an alert channel via `/uc-config`.
- Prevent duplicate notifications and diagnose operational errors.

We never sell, rent, or share user data for advertising, profiling, or marketing, and we do not train machine-learning models on your data.

## 4. Third-Party Services and Subprocessors

Chatisimo interacts only with the following external services:

- **Discord API**: delivers slash-command replies, interactive modals/components, configured server-channel alerts, and participant direct messages.
- **Google Cloud Platform (Compute Engine & Vertex AI / Gemini)**: hosts the production virtual machine and PostgreSQL database. When LLM features are enabled:
  - Anonymous gift-exchange questions and answers are sent to Vertex AI (Gemini) solely to rephrase writing style while preserving meaning before storage and delivery.
  - Public match-schedule fields (opponent, venue, competition, and kickoff window, never Discord chat history) are sent to Vertex AI (Gemini) to generate short banter lines.
- **ESPN Public Sports API**: queried for public Universidad Católica match schedules. No Discord user IDs, server IDs, or user content are sent to ESPN.

## 5. Data Security and Retention

- **Security**: the production database runs on an isolated private container network accessible only from the host VM behind Google Cloud Identity-Aware Proxy (IAP). Application secrets stored in PostgreSQL are encrypted at rest with AEAD keys derived from a separate `MASTER_KEY`.
- **Retention**: gift-exchange records and `/uc-config` settings are kept while needed to operate active exchanges, preserve `avoid_last_round` pairings between consecutive rounds in the same server, and deliver scheduled alerts.

## 6. Self-Service Controls and Data Deletion Requests

You can remove data directly from Discord at any time:

- **Clear UC server settings**: a member with `Manage Server` can run `/uc-config clear` (for `all`, `friend`, `channel`, or `locale`) to erase stored UC settings for that server immediately.
- **Clear gift preferences or leave an exchange**: any participant can overwrite or blank out their `likes`, `dislikes`, and `details` via `/ge preferences`, or leave an open exchange via `/ge leave`; an organizer can cancel an exchange via `/ge cancel`.
- **Full erasure request**: to request permanent deletion of all stored database records associated with your Discord user ID or a Discord server you manage, open an issue at [github.com/HectorVeraO/chatisimo/issues](https://github.com/HectorVeraO/chatisimo/issues) stating your Discord user ID or server ID (never include tokens or passwords). Verified deletion requests are completed within 30 days.

## 7. Changes to This Policy

We may update this Privacy Policy when Chatisimo's features or data practices change. Revisions will be posted at this URL with an updated effective date at the top of the document.

## 8. Contact

For privacy questions or data deletion requests, open an issue at [github.com/HectorVeraO/chatisimo/issues](https://github.com/HectorVeraO/chatisimo/issues).

---

[Home](./) · [Terms of Service](./terms) · [GitHub Repository](https://github.com/HectorVeraO/chatisimo)
