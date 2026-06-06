package clock

import "time"

// Real is the production clock backed by time.Now.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }
