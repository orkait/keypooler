package api

const (
	// defaultWindowSeconds is the rate window of a tier feature that names none.
	defaultWindowSeconds = 60
	// The /admin/usage page size when none is asked for, and the most one call returns.
	defaultUsageListLimit = 100
	maxUsageListLimit     = 1000
	// exhaustedEvent is the audit feature name recorded when a consumer reports a key spent.
	exhaustedEvent = "exhausted"
)
