package clock

import "time"

// Clock abstracts time.Now() to allow deterministic tests.
type Clock interface {
	Now() time.Time
}
