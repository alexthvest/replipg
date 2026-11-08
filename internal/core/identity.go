// Package core contains shared identities and transport-neutral output values.
package core

import "errors"

type GroupID int64

type UserID int64

type ID string

type Scope struct {
	Group    GroupID
	Actor    UserID
	BotID    int64
	UpdateID int64
}

func (s Scope) Validate() error {
	if s.Group >= 0 || s.Actor <= 0 {
		return errors.New("invalid group or actor")
	}
	return nil
}

type User struct {
	ID        UserID
	Username  string
	FirstName string
	LastName  string
}
