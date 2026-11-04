# Requirements Document

## Introduction

The Telegram Runtime provides the operational foundation for Replipg's group-based gameplay. It allows one bot instance to start safely, receive and route Telegram group interactions, deliver public and user-specific responses, preserve application state, and recover scheduled and outbound work after interruption. Gameplay specifications consume these capabilities but retain ownership of character, inventory, encounter, and battle behavior.

## Boundary Context

- **In scope**: Operator-supplied configuration, startup validation, single-instance update intake, command and callback routing, ordinary group-message intake, public and user-specific ephemeral responses, callback acknowledgement, durable state operations and upgrades, scheduled work, outbound delivery recovery, retry behavior, privacy-safe diagnostics, and graceful shutdown.
- **Out of scope**: Gameplay rules and player-facing command semantics, character or inventory behavior, activity scoring, encounter probability, combat, content administration, private-chat experiences, inbound webhook operation, multi-instance coordination, and horizontally scaled operation.
- **Adjacent expectations**: Gameplay features register behavior against runtime capabilities and are responsible for their own domain validation and idempotent state transitions. Operators provide valid Telegram credentials, a supported bot configuration, and a local persistent storage location, and run exactly one active runtime instance.

## Requirements

### Requirement 1: Safe Startup and Configuration
**Objective:** As an operator, I want startup to validate its prerequisites before serving traffic, so that configuration and state problems fail visibly instead of causing partial operation.

#### Acceptance Criteria
1. When the Telegram Runtime starts, the Telegram Runtime shall validate all required operator-supplied configuration before accepting updates or dispatching work.
2. If required configuration is missing or invalid, the Telegram Runtime shall terminate startup with a non-success status and identify the invalid setting without exposing its sensitive value.
3. When configuration is valid, the Telegram Runtime shall prepare durable state, apply all pending compatible state upgrades, disable any conflicting inbound update delivery, and start update intake before reporting readiness.
4. If durable state preparation, a state upgrade, conflicting delivery cleanup, or update intake initialization fails, the Telegram Runtime shall remain not ready and terminate startup with an actionable diagnostic.
5. The Telegram Runtime shall obtain Telegram updates without requiring an inbound public network endpoint.
6. The Telegram Runtime shall support exactly one active runtime instance for a deployment and shall reject unsupported deployment modes.

### Requirement 2: Update Intake and Routing
**Objective:** As a gameplay feature developer, I want relevant Telegram group interactions routed consistently, so that domain behavior can respond without owning bot update intake.

#### Acceptance Criteria
1. While the Telegram Runtime is ready, the Telegram Runtime shall continuously receive group commands, callback interactions, and ordinary group messages made available by Telegram.
2. When a received update matches registered behavior, the Telegram Runtime shall dispatch it with the update, chat, user, and interaction identity needed to process it.
3. When a callback interaction is received, the Telegram Runtime shall acknowledge the interaction independently of any longer-running feature behavior.
4. If a received update is unsupported or has no registered behavior, the Telegram Runtime shall skip domain processing without interrupting subsequent update intake.
5. If registered behavior fails while processing an update, the Telegram Runtime shall isolate the failure, report it with a correlation identifier, and continue receiving subsequent updates.
6. When an update already recorded as completed is received again, the Telegram Runtime shall not repeat its registered state-changing processing.
7. If update intake is temporarily interrupted, the Telegram Runtime shall resume from the last durably completed update without intentionally discarding later updates.

### Requirement 3: Public and User-Specific Group Responses
**Objective:** As a Telegram group participant, I want interactions to use the appropriate visibility, so that shared events remain public while personal interfaces remain private within the group.

#### Acceptance Criteria
1. When a feature requests a public response, the Telegram Runtime shall deliver the response to the specified group chat for all permitted group members to view.
2. When a feature requests a user-specific ephemeral response, the Telegram Runtime shall deliver the response in the specified group with visibility limited to the specified user.
3. If a user-specific ephemeral response cannot be delivered with restricted visibility, the Telegram Runtime shall report delivery failure and shall not fall back to a public response.
4. When a feature requests callback acknowledgement, the Telegram Runtime shall complete the acknowledgement without requiring a visible chat message.
5. If a response request lacks a valid destination, target user where required, or supported content, the Telegram Runtime shall reject it before attempting delivery and shall return a specific failure reason.
6. The Telegram Runtime shall not initiate private-chat flows as a fallback for group interactions.

### Requirement 4: Recoverable Outbound Delivery
**Objective:** As a gameplay feature developer, I want outbound delivery intent to survive failures and restarts, so that a process interruption does not silently lose user-visible outcomes.

#### Acceptance Criteria
1. When a feature requests recoverable outbound delivery, the Telegram Runtime shall durably record the delivery intent before the first delivery attempt.
2. When Telegram definitively accepts a recoverable delivery, the Telegram Runtime shall record it as completed and shall not schedule it for another delivery.
3. If Telegram reports a transient delivery failure, the Telegram Runtime shall retain the delivery intent and retry it according to a bounded retry policy.
4. If Telegram supplies a retry delay, the Telegram Runtime shall not retry the delivery before that delay has elapsed.
5. If Telegram reports a permanent delivery failure, the Telegram Runtime shall stop automatic retries and retain an operator-observable failure reason.
6. If a delivery attempt has an indeterminate outcome, the Telegram Runtime shall mark it as requiring reconciliation and shall not blindly resend it as though no attempt occurred.
7. When the Telegram Runtime restarts, the Telegram Runtime shall resume pending retries and reconciliation work without retrying deliveries already recorded as completed.
8. While shutdown or cancellation is in progress, the Telegram Runtime shall stop retry waits promptly and preserve every incomplete recoverable delivery for later recovery.

### Requirement 5: Durable State and Atomic Operations
**Objective:** As a gameplay feature developer, I want durable and atomic state operations, so that game state remains consistent across failures and restarts.

#### Acceptance Criteria
1. When a feature successfully stores state, the Telegram Runtime shall make that state available after a process restart using the same persistent storage.
2. When a feature requests an atomic state operation, the Telegram Runtime shall make either all changes in that operation visible or none of them visible.
3. If an atomic state operation fails, the Telegram Runtime shall leave no partial changes from that operation visible.
4. If storage remains unavailable or contended beyond the configured bounded wait, the Telegram Runtime shall return a distinguishable failure instead of waiting indefinitely.
5. When a pending state upgrade completes successfully, the Telegram Runtime shall record its completion so that later startups do not apply it again.
6. If a state upgrade fails, the Telegram Runtime shall stop startup and shall not report readiness with a partially upgraded state.
7. The Telegram Runtime shall keep its primary persistent state and associated recovery files within the operator-configured local persistent storage location.

### Requirement 6: Durable Scheduled Work
**Objective:** As a gameplay feature developer, I want scheduled work to be durable, so that due game activity survives restarts and executes predictably.

#### Acceptance Criteria
1. When a feature schedules work, the Telegram Runtime shall durably retain its identity, due time, execution state, and feature-supplied payload before confirming the schedule request.
2. While scheduled work is pending, the Telegram Runtime shall not make it eligible for execution before its due time.
3. When scheduled work becomes due, the Telegram Runtime shall permit no more than one concurrent execution claim within the active runtime instance.
4. When the Telegram Runtime starts, the Telegram Runtime shall make overdue incomplete work eligible for execution.
5. When scheduled work completes successfully, the Telegram Runtime shall record completion before considering the work finished and shall not execute it again.
6. When scheduled work is cancelled before completion, the Telegram Runtime shall record cancellation and shall not execute it afterward.
7. If scheduled work fails with a retryable outcome, the Telegram Runtime shall retain it and make it eligible again according to its registered retry policy.
8. If scheduled work fails with a permanent outcome, the Telegram Runtime shall stop automatic execution attempts and retain an operator-observable failure reason.

### Requirement 7: Graceful Shutdown and Restart Recovery
**Objective:** As an operator, I want controlled shutdown and recovery, so that deployments do not lose accepted work or leave the runtime in an ambiguous operating state.

#### Acceptance Criteria
1. When a shutdown signal is received, the Telegram Runtime shall stop accepting new update work and emit a draining lifecycle diagnostic.
2. While shutdown is in progress, the Telegram Runtime shall allow already-running work to finish only within the configured shutdown deadline.
3. When the shutdown deadline expires, the Telegram Runtime shall cancel remaining in-process work and preserve its durable work state for recovery.
4. When shutdown completes, the Telegram Runtime shall release update intake, storage, timers, and other acquired resources.
5. When the Telegram Runtime restarts after interruption, the Telegram Runtime shall recover incomplete scheduled work and outbound delivery work from durable state before reporting readiness.
6. If an interruption leaves an outbound attempt without a definitive result, the Telegram Runtime shall recover it as reconciliation-required rather than completed or never attempted.

### Requirement 8: Operational Diagnostics
**Objective:** As an operator, I want accurate lifecycle diagnostics, so that I can distinguish a running process from one ready to serve and investigate failures safely.

#### Acceptance Criteria
1. When the runtime changes lifecycle phase, the Telegram Runtime shall emit a structured diagnostic with the phase (`starting`, `recovering`, `ready`, `draining`, or `stopped`).
2. While startup is incomplete or a startup prerequisite fails, the Telegram Runtime shall not signal startup completion; during shutdown it shall reject new work admission.
3. When recovery and worker/polling startup handshakes complete, the Telegram Runtime shall close its in-process `Ready()` channel and emit the `ready` lifecycle diagnostic.
4. When startup, update processing, scheduled work, or outbound delivery changes state or fails, the Telegram Runtime shall emit an operator-observable diagnostic containing an event category and correlation identifier.
5. The Telegram Runtime shall preserve distinct durable-work states and emit operation outcomes through privacy-safe diagnostics; automated verification shall inspect persisted states directly. No health HTTP server, status endpoint, or aggregate-status API is required.
6. The Telegram Runtime shall not expose bot credentials, secret configuration values, personal message bodies, or private response content in logs or error diagnostics.
7. If a diagnostic includes Telegram user, chat, or update identifiers, the Telegram Runtime shall limit them to the minimum identifiers required for operational correlation.

### Requirement 9: Feature Boundaries and Compatibility
**Objective:** As a gameplay feature developer, I want stable runtime capabilities without embedded game rules, so that gameplay specifications can evolve independently of Telegram transport and persistence concerns.

#### Acceptance Criteria
1. The Telegram Runtime shall provide update routing, response delivery, durable state, atomic operation, scheduling, and recovery capabilities without defining character, inventory, encounter, battle, or content-administration rules.
2. When a gameplay feature registers commands, callbacks, scheduled work, or outbound delivery, the Telegram Runtime shall preserve the feature's domain result without interpreting its gameplay meaning.
3. The Telegram Runtime shall not define player-facing game commands beyond the behavior required to route a registered command to its owning feature.
4. The Telegram Runtime shall not provide multi-instance coordination or horizontal scaling behavior.
5. Where ordinary group messages are needed by an adjacent feature, the Telegram Runtime shall make received message events available without assigning activity scores or encounter outcomes.
