package domain

import "errors"

var (
	// ErrNotFound is returned when an entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is returned when an entity breaks a domain invariant.
	ErrInvalid = errors.New("invalid")
	// ErrNoStorageCapacity is returned when every storage slot is full.
	ErrNoStorageCapacity = errors.New("no storage capacity available")
	// ErrConflict is returned on an illegal state transition or concurrent claim.
	ErrConflict = errors.New("conflict")
)
