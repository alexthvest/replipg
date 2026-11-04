# Requirements Document

## Introduction

Menu Sharing lets players deliberately show the group what they are inspecting without making routine browsing public. A Share action publishes an attributed, fixed, read-only text snapshot of the displayed character summary, inventory page, or item details in the same group. Players may share the same view again after a 30-second cooldown; earlier shares remain in the conversation.

## Boundary Context

- **In scope**: Explicit sharing from supported personal views; exact displayed-content preservation; owner attribution; same-group publication; repeat sharing with a 30-second cooldown; authorization, stale controls, delivery outcomes, English/Russian presentation, and reproducible verification.
- **Out of scope**: Deleting or replacing earlier shares; public interactive menus; menu visibility settings; automatic sharing; cross-group or private-chat sharing; images; public settings or development menus; new gameplay mechanics.
- **Adjacent expectations**: Existing menus remain ephemeral and retain their existing authorization and usability rules. Sharing does not create or modify gameplay state, change group-language authority, or introduce a public fallback for failed personal delivery. Existing menu expiry still applies; Share has no additional five-minute expiry of its own.
- **Confirmed product decisions**: Preserve the displayed view rather than fetch fresh state; allow repeat sharing after 30 seconds; retain previous public shares; omit deletion from this feature.

## Requirements

### Requirement 1: Explicit Sharing from Personal Views
**Objective:** As a player, I want to share the view I am inspecting, so that I can show others my character, inventory, or an item without exposing routine browsing.

#### Acceptance Criteria
1. When a supported personal character summary, inventory page, or item-details view is displayed, the Menu Sharing System shall provide a labeled Share action for that view.
2. When an authorized owner invokes an available Share action, the Menu Sharing System shall request publication of that view's snapshot as a new public message in the originating group.
3. When a player opens, navigates, equips, or unequips through a personal menu without invoking Share, the Menu Sharing System shall leave those interactions personal and publish no snapshot.
4. When sharing is processed, the Menu Sharing System shall leave character creation, health, recovery, starter entitlement, inventory quantities, and equipment unchanged.
5. The Menu Sharing System shall exclude settings and development menus from shareable views.

### Requirement 2: Exact, Read-Only Public Snapshots
**Objective:** As a player, I want the public copy to show what I chose to share, so that it cannot silently expose different or newer state.

#### Acceptance Criteria
1. When a snapshot is published, the Menu Sharing System shall preserve the source view's displayed body, formatting, item selection or page, numeric values, and language, excluding its interactive controls.
2. If gameplay state, item presentation, or group language changes after a view is displayed, the Menu Sharing System shall share the captured displayed content rather than substitute newly rendered state.
3. When a snapshot is published, the Menu Sharing System shall identify its owner with a user-identifying attribution, including when the owner has no username.
4. The Menu Sharing System shall omit navigation, equipment, Share, and other interactive controls from public snapshots.
5. When subsequent browsing, gameplay, or language changes occur, the Menu Sharing System shall leave previously published snapshots unchanged.
6. When a public snapshot is prepared, the Menu Sharing System shall fit its body and attribution within supported message and formatting limits without silently dropping displayed content.
7. If an exact attributed snapshot cannot be published within supported limits, the Menu Sharing System shall refuse publication and provide personal guidance when personal feedback remains authorized.

### Requirement 3: Repeat Sharing and Duplicate Suppression
**Objective:** As a player, I want to bring a snapshot back into an active conversation without accidental repeated presses creating clutter.

#### Acceptance Criteria
1. When a view is successfully shared, the Menu Sharing System shall prevent a further share of that same displayed view by its owner in that group until 30 seconds have elapsed after the successful publication.
2. When the owner presses Share during that cooldown, the Menu Sharing System shall publish no additional snapshot and provide personal guidance indicating when sharing is available again, when personal feedback remains authorized.
3. When at least 30 seconds have elapsed after the last successful publication and the source menu remains usable, the Menu Sharing System shall permit a new deliberate Share action for that same view.
4. When a new deliberate share succeeds, the Menu Sharing System shall retain all previous public shares without deletion, replacement, or editing.
5. When the same incoming Share action is delivered repeatedly, the Menu Sharing System shall treat it as one action and create no additional publication request for its replays.
6. While a Share action for a displayed view is awaiting a delivery outcome, the Menu Sharing System shall prevent concurrent presses for that view from creating additional publication requests.
7. If publication is definitively rejected without sending a message, the Menu Sharing System shall allow the owner to retry without treating the rejected publication as a successful share or starting its cooldown.

### Requirement 4: Owner, Group, and View Authorization
**Objective:** As a player, I want only my deliberate action to expose my view in its original group.

#### Acceptance Criteria
1. When a Share action is processed, the Menu Sharing System shall verify the individual human owner, originating group, and associated displayed view before permitting public publication.
2. If a Share action comes from another actor or group, or contains malformed, unknown, or unusable control data, the Menu Sharing System shall reject publication without exposing the snapshot or changing gameplay state.
3. The Menu Sharing System shall use authenticated player identity rather than names, usernames, or supplied ownership claims to authorize sharing.
4. While the source menu and its Share control remain usable under the menu's existing rules, the Menu Sharing System shall impose no separate five-minute Share expiry.
5. If the source menu or Share control is no longer usable, the Menu Sharing System shall refuse publication and guide the owner to reopen the menu when personal feedback remains authorized.
6. If a view update fails or its displayed result cannot be established, the Menu Sharing System shall not publish content that differs from the view associated with the invoked Share control.

### Requirement 5: Delivery Outcomes and Restart Behavior
**Objective:** As a player, I want sharing feedback to reflect what was actually delivered, so that failures do not mislead me or cause uncontrolled reposts.

#### Acceptance Criteria
1. When public delivery is confirmed successful, the Menu Sharing System shall provide a personal success indication when personal feedback remains authorized.
2. If public delivery is definitively rejected, the Menu Sharing System shall provide personal failure guidance when personal feedback remains authorized and shall not claim the snapshot was shared.
3. If public delivery has an uncertain outcome, the Menu Sharing System shall distinguish that uncertainty from confirmed success or confirmed failure whenever it can provide personal outcome feedback.
4. If personal feedback delivery fails after public sharing succeeds, the Menu Sharing System shall retain the successful-share outcome without requesting another public post to compensate for the feedback failure.
5. If personal view delivery fails without an authorized Share action, the Menu Sharing System shall publish no public snapshot as a fallback.
6. When the application restarts, the Menu Sharing System shall preserve accepted Share action identities and unelapsed cooldowns so that replay or restart does not itself authorize an additional post.
7. When a delivery operation is retried or recovered, the Menu Sharing System shall preserve its authorized destination, owner attribution, and captured snapshot content.

### Requirement 6: Localization and Data Handling
**Objective:** As a player or operator, I want sharing to follow the existing language and privacy behavior.

#### Acceptance Criteria
1. When a Share control or new personal sharing guidance is rendered, the Menu Sharing System shall use the group's current English or Russian language selection.
2. When previously captured content is shared after a group-language change, the Menu Sharing System shall preserve that captured content's language rather than translate the snapshot again.
3. The Menu Sharing System shall exclude snapshot bodies, player display labels, credentials, and personal interaction content from logs and operational diagnostics.
4. When retained snapshots no longer support usable controls or unresolved delivery work, the Menu Sharing System shall remove their retained private content within a documented finite retention period rather than retain a permanent snapshot archive.
5. The Menu Sharing System shall leave administrator-owned group-language settings and command-menu visibility unchanged.

### Requirement 7: Reproducible Verification
**Objective:** As an operator, I want verifiable sharing milestones, so that the feature can be assessed without relying solely on a live group.

#### Acceptance Criteria
1. The Menu Sharing System shall support deterministic automated verification of exact-view capture, owner/group rejection, cooldown boundaries, repeated actions, concurrent presses, failure outcomes, restart behavior, and both supported languages without contacting real Telegram.
2. When an implementation milestone is delivered, the Menu Sharing System shall provide runnable reproduction instructions and expected observations for sharing in a Telegram test group.
3. When verification results are documented, the Menu Sharing System shall distinguish automated evidence from live Telegram observations and identify live checks that have not been performed.
4. The Menu Sharing System shall provide regression evidence that personal browsing, equipment management, and failed personal-delivery behavior retain their existing contracts.
