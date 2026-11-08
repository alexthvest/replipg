# Spec Delta

## ADDED Requirements

### Requirement: Sale reservations preserve ownership until settlement
Inventory SHALL distinguish owned, reserved, and available quantities. Reservations MUST be durable, group and character scoped, and tied to one sale order; total reserved quantity MUST NOT exceed ownership. A reserved individual instance MUST be unavailable to another sale. Reserving items MUST NOT modify their snapshots, identities, or owned quantities.

#### Scenario: Reserve part of a stack
- **WHEN** a player reserves 3 units of an owned stack of 10
- **THEN** ownership remains 10, reserved quantity is 3, and available quantity is 7
- **AND** inventory inspection and sale selection expose the reservation without showing it as completed removal

#### Scenario: Restart with an unresolved payout
- **WHEN** the process restarts while a sale payout is unresolved
- **THEN** its reserved quantities remain unavailable and retain their original item snapshots and identities

### Requirement: Sale settlement changes ownership atomically
Confirmed sale payout SHALL reduce the exact reserved stack quantity or remove the reserved individual instance, release its reservation, and complete the order in one transaction. Established nonpayment MUST release the reservation without changing ownership. Duplicate settlements MUST have no further effect. Inventory views and runtime ownership MUST reflect removal as well as addition.

#### Scenario: Complete partial stack sale
- **WHEN** a verified payout settles the reserved 3 units of a stack of 10
- **THEN** owned quantity becomes 7 and the reservation disappears together with the completed order result

#### Scenario: Settlement commit fails
- **WHEN** persistence fails during ownership removal and order completion
- **THEN** neither partial removal nor completed status commits and the recorded payout remains recoverable

#### Scenario: Entire entry sold
- **WHEN** the last units or an individual instance are sold successfully
- **THEN** the entry disappears from persistent ownership and runtime views while its snapshot remains available to historical orders
