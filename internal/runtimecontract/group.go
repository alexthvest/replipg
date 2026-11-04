package runtimecontract

import (
	"context"
	"time"
)

// MembershipStatus is current, verified group membership evidence.
type MembershipStatus uint8

const (
	Unknown MembershipStatus = iota
	Member
	Administrator
	Owner
)

type MembershipResult struct {
	Status    MembershipStatus
	CheckedAt time.Time
}
type GroupMembershipReader interface {
	VerifyGroupMember(context.Context, ChatID, UserID) (MembershipResult, error)
}
type GroupMenuWriter interface {
	ReplaceGroupMenu(context.Context, ChatID, []CommandDeclaration) error
}

// MenuFailureKind classifies remote failures without retaining provider text.
type MenuFailureKind uint8

const (
	Transient MenuFailureKind = iota
	RateLimited
	Forbidden
	Invalid
	Indeterminate
)

type MenuError struct {
	Kind       MenuFailureKind
	RetryAfter time.Duration
}

func (e *MenuError) Error() string {
	switch e.Kind {
	case RateLimited:
		return "group menu: rate limited"
	case Forbidden:
		return "group menu: forbidden"
	case Invalid:
		return "group menu: invalid"
	case Indeterminate:
		return "group menu: indeterminate"
	default:
		return "group menu: transient"
	}
}
