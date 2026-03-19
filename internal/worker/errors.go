package worker

import (
	"errors"
)

var (
	ErrPermanent = errors.New("permanent error: do not retry")
	ErrTransient = errors.New("transient error: requeue task")
)
