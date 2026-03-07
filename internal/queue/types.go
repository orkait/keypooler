package queue

// Item represents a unit of work in the queue.
type Item struct {
	ExecutionID string
	Feature     string // which feature this execution needs (for key selection)
}
