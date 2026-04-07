package util

import (
	"context"
	"time"
)

const (
	DBTimeoutShort = 2 * time.Second
	DBTimeoutLong  = 5 * time.Second
)

func DBContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeout)
}
