package auth

import (
	"context"
	"errors"
)

var (
	ErrInvalidToken = errors.New("invalid authentication token")
	ErrUnavailable  = errors.New("authentication service unavailable")
)

type Identity struct {
	ID          string
	Email       *string
	DisplayName string
	PhotoURL    *string
}

type Verifier interface {
	Verify(context.Context, string) (Identity, error)
}
