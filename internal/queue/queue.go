package queue

import (
	"errors"
	"sync"
)

var (
	ErrQueueFull   = errors.New("queue is full")
	ErrQueueClosed = errors.New("queue is closed")
)

// Queue is a bounded work queue backed by a buffered channel.
type Queue struct {
	ch     chan *Item
	mu     sync.Mutex
	closed bool
}

// NewQueue creates a queue with the given capacity.
func NewQueue(maxSize int) *Queue {
	return &Queue{
		ch: make(chan *Item, maxSize),
	}
}

// Enqueue adds an item to the queue. Non-blocking.
func (q *Queue) Enqueue(item *Item) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return ErrQueueClosed
	}

	select {
	case q.ch <- item:
		return nil
	default:
		return ErrQueueFull
	}
}

// Dequeue returns the channel that workers read from.
func (q *Queue) Dequeue() <-chan *Item {
	return q.ch
}

// Size returns the current number of items in the queue.
func (q *Queue) Size() int {
	return len(q.ch)
}

// Close shuts down the queue.
func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
}
