package common

import "time"

// MaxRetriesBeforeHalt is the maximum number of retries before halting.
const MaxRetriesBeforeHalt = 5

// MaxRetriesTimeout is the maximum time to wait before halting.
const MaxRetriesTimeout = 5 * time.Second
