package robots

import "errors"

var (
	ErrAccessDenied = errors.New("access denied by server (4xx)")
	ErrServerError  = errors.New("target server error (5xx)")
)
