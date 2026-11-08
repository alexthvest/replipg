package core

import (
	"crypto/rand"
	"encoding/hex"
)

func NewID() (ID, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return ID(hex.EncodeToString(bytes[:])), nil
}
