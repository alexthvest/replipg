# Spec Delta

## Purpose

Exchange authenticated bank requests and reconcile financial outcomes against the external public contract without duplicating player charges or treasury payouts.

## ADDED Requirements

### Requirement: Bank messages use an authenticated private route
The integration SHALL exchange JSON-RPC 2.0 text in the configured bank bot's private chat. Replies MUST match its verified numeric sender and chat identities and a persisted outstanding request ID. Forwarded messages, untrusted senders, unsolicited replies, and malformed envelopes MUST NOT settle orders. Ordinary private gameplay MUST remain unsupported.

#### Scenario: Forged payment reply
- **WHEN** a user, another bot, or a forwarded message supplies a plausible paid result
- **THEN** no order, item ownership, or reservation changes

#### Scenario: Bank reply arrives during another gameplay action
- **WHEN** an authenticated correlated reply arrives while gameplay is processing
- **THEN** it is handled without requiring that action to synchronously wait for the bank

### Requirement: Requests conform to the existing public protocol
The integration SHALL use only documented `.public.v1` balance, history, transfer, payment request, and payment status methods and their exact parameter envelopes. It MUST preserve integer currency precision, enforce documented sizes and limits, and use Telegram user IDs rather than usernames for player selection. It MUST NOT require issuance, management, push notifications, or an operation-status API.

#### Scenario: Amount exceeds bank limits
- **WHEN** an order total exceeds 100 decimal digits
- **THEN** the order is rejected before any financial request is sent

#### Scenario: Invoice delivery fails
- **WHEN** delivery to the originating group does not produce a resolved outcome
- **THEN** the integration retains the original chatId and parameters during recovery rather than redirecting the invoice

### Requirement: Financial intent is durable before sending
Every financial request SHALL have a durable order, immutable financial payload, financial idempotency key, and transport request identity before transmission. Retries MUST preserve the financial key and payload. A transport ID MUST NOT be reused for a different request; status reads MUST use fresh IDs so an earlier pending response cannot mask later settlement.

#### Scenario: Process stops after financial effect but before reply
- **WHEN** the bank applies a transfer and the game restarts without recording the reply
- **THEN** recovery retries the original transfer with the same financial key and payload
- **AND** one bank transaction corresponds to the sale

#### Scenario: Invoice creation reply is lost
- **WHEN** an invoice may exist but its requestId has not been saved
- **THEN** recovery repeats creation with the original financial key and parameters to recover that invoice

### Requirement: Financial proof is validated before fulfillment
The integration SHALL validate correlated request identity, state, expected amount, expected treasury direction, bank transaction identity, and available counterparty identity against persisted intent before applying settlement. Paid invoice responses MUST contain matching transaction data. Duplicate or contradictory responses MUST NOT repeat or reverse completed ownership mutations.

#### Scenario: Successful operation without payment
- **WHEN** invoice creation returns an operationId and pending state
- **THEN** the order stays unpaid and later checks use payment status

#### Scenario: Mismatched transaction
- **WHEN** a paid result names another amount or destination account
- **THEN** fulfillment is blocked and the order requires reconciliation

#### Scenario: Old pending response arrives after completion
- **WHEN** a previously delayed pending reply follows a verified paid and fulfilled outcome
- **THEN** the order remains completed and no new items or requests are created

### Requirement: Unknown outcomes remain recoverable
The integration SHALL distinguish JSON-RPC errors, financial failed/rejected results, timeouts, and invalid replies. A later error after an unresolved financial attempt MUST NOT prove nonpayment. Inconclusive cases MUST retain intent and sale reservations, expose reconciliation-required status, and permit safe recovery using the original financial key. Balance changes and approximate history matches MUST NOT be treated as order proof.

#### Scenario: Permissions revoked after a lost payout reply
- **WHEN** retrying an unresolved transfer returns Method unavailable
- **THEN** the items remain reserved and the order is not marked unpaid solely from that error

#### Scenario: Request conflict
- **WHEN** the bank reports a transport or financial payload conflict
- **THEN** the integration stops automatic mutation retries for that order and reports reconciliation required without replacing the key

### Requirement: Recovery is bounded and survives interruption
The integration SHALL resume outstanding orders on startup, retry transient failures with bounded backoff and rate limits, and poll pending invoices without assuming push delivery. Shutdown MUST stop new financial work and retain enough durable state to resume every in-flight request. Bank unavailability MUST NOT block unrelated gameplay.

#### Scenario: Bank remains offline
- **WHEN** bank replies are unavailable for an extended period
- **THEN** outstanding orders remain visible, retries are rate limited, and character and inventory browsing continue

#### Scenario: Shutdown during transmission
- **WHEN** shutdown interrupts a financial send
- **THEN** restart reconciles its possible effect using persisted intent rather than creating a replacement transaction
