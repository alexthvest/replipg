# Brief: menu-sharing

## Problem

Players in the Telegram RPG group cannot easily show others what they are inspecting in personal character and inventory menus. The user wants more visible social activity, but making all browsing and equipment interactions public would overload the conversation.

## Current State

Character, inventory, and settings interfaces are ephemeral. Inventory navigation, item details, and equipment changes edit the originating personal message. Controls are bound to the actor and group; failed personal delivery has no public or private-chat fallback.

The runtime already supports public group messages and ephemeral-origin callbacks. Inventory controls retain view coordinates and revisions, but neither character nor inventory retains the exact rendered view for later sharing. Character currently has no Share callback. Group settings currently manage administrator-controlled language, not player visibility preferences.

## Desired Outcome

A player can deliberately share the currently displayed character summary, inventory page, or item details with the same group. The public message identifies the owner and contains a fixed, read-only text snapshot of that view. Browsing and equipment management remain personal. Later gameplay, navigation, language changes, and equipment changes do not update an existing shared message.

## Approach

Add an explicitly labeled Share button to supported personal views. Capture the displayed text and formatting when producing each view, and bind the Share action to that immutable snapshot, its owner, and the originating group. On an authorized Share action, publish a fresh public message containing that snapshot and owner attribution, without navigation, equipment, or Share controls. A snapshot is a Telegram text message, not a screenshot or generated image.

The user selected sharing the current view over a compact summary that ignores the open page. It supports showing a particular item or inventory page while limiting public messages to deliberate player actions. A menu visibility setting and public interactive menus were considered but deferred because routine browsing would clutter the conversation.

A delegated local viability check found no implementation blocker. Existing public-send delivery and ephemeral callback mapping can support this approach without a new dependency or SDK upgrade. Live Telegram delivery and external dependency maintenance/security were not verified during discovery; implementation must demonstrate delivery in a test group.

## Scope

- **In**: Share controls on character summaries, inventory pages, and item details; exact displayed-content capture; same-group public publication; owner attribution; immutable read-only public messages; owner/group authorization; bounded control lifetime and replay handling; localized controls and guidance; delivery outcome handling; deterministic tests and documented Telegram demonstrations.
- **Out**: Public interactive menus; personal or group visibility settings; automatic publication of equipment changes or browsing; private-chat or cross-group sharing; images; live-updating public cards; public settings or development menus; new gameplay mechanics.

## Boundary Candidates

- Shared view snapshot and Share authorization lifecycle, including bounded retention, expiry, owner/group binding, and accepted-action identity.
- Character and inventory presentation integration, attaching Share to the exact view that was rendered without changing gameplay ownership.
- Public snapshot projection and delivery through existing runtime contracts, including attribution, formatting, size validation, and truthful outcome guidance.

These are seams inside one feature, not separate specifications or services.

## Out of Boundary

- Character and inventory retain ownership of health, recovery, initialization, starter grants, equipment, quantities, and gameplay operations. Sharing must not refresh gameplay state or trigger those operations.
- Runtime retains ownership of Telegram transport, callback acknowledgement, delivery recovery, and lifecycle behavior.
- Localization retains ownership of group language and translation catalogs. Group language authority and settings behavior do not change.
- Existing personal-response failure rules remain intact. Only an explicit, authorized Share action permits public publication.

## Upstream / Downstream

- **Upstream**: telegram-runtime supplies authenticated actor/group context, callback routing, public delivery, persistence, and delivery recovery capabilities. character-system and inventory-equipment supply the personal views. localization supplies English/Russian presentation and group locale resolution.
- **Downstream**: Future gameplay interfaces may reuse the sharing capability if separately specified. Encounter and battle implementation are not prerequisites or part of this feature.

## Existing Spec Touchpoints

- **Extends**: No separate existing-spec workstream; this new feature owns explicit sharing and its integration into existing views.
- **Adjacent**: Character and inventory privacy requirements need a narrow explicit-sharing exception while preserving personal browsing, owner-only management, and absence of delivery fallback. Runtime public delivery is reused. Localization catalogs gain Share text and guidance. Settings and command-menu visibility remain unchanged.

## Constraints

- Confirmed direction: sharing the current view, same-group publication, owner attribution, read-only public copies, and no automatic updates. The user chose sharing to avoid conversation overload.
- Confirmed during requirements discussion: the same displayed view may be shared again after a 30-second cooldown; repeated presses during that cooldown do not publish another copy. Sharing has no separate five-minute expiry and remains available while the source menu is usable under its existing rules.
- Confirmed during requirements discussion: previous public shares remain in the conversation. Deleting or replacing earlier shares was considered and explicitly excluded.
- Preserve the displayed body and its language even if gameplay state, content definitions, or group locale changes before Share is pressed. Do not silently substitute freshly rendered state. Define attribution and any surrounding localized text during requirements/design.
- Use actor IDs for authorization and validated group binding for destination. Names and usernames are display labels only. Define attribution for players without usernames without introducing profile lookups as a prerequisite.
- Snapshot capture and control lifecycle must account for failed or ambiguous personal-message edits so an active Share control cannot publish content different from the view it belongs to.
- Define snapshot lifetime, cleanup, stale-view behavior, repeated deliberate presses, duplicate updates, restart behavior, and failure feedback during requirements/design. Avoid an indefinite snapshot archive or uncontrolled retained profile data; do not log snapshot bodies.
- Define delivery intent, retry policy, and ambiguous-send behavior explicitly. Telegram delivery and SQLite cannot commit atomically; do not promise exactly-once public publication. Prefer existing recovery capabilities where appropriate.
- Reserve space for attribution within existing Telegram text limits and preserve valid formatting/entity offsets. Public copies contain no callback controls.
- Preserve the pinned Go/SQLite single-replica architecture, existing package dependency rules, and devenv workflow. Any persistence change requires a compatible additive migration and operational documentation.
- Deliver runnable vertical milestones with deterministic tests for exact-content capture, authorization, stale controls, duplicate actions, delivery failures, restart behavior, both languages, and unchanged personal browsing. Record a live Telegram demonstration separately from automated evidence.
