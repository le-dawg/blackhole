// src/dnsd/ringbuffer.go
package dnsd

import (
	"sync"
	"time"
)

type QueryRecord struct {
	Timestamp   time.Time `json:"timestamp"`
	Domain      string    `json:"domain"`
	QueryType   uint16    `json:"queryType"`
	Status      string    `json:"status"`
	ProcessName string    `json:"processName"`
	BundleID    string    `json:"bundleId"`
	LatencyMs   float64   `json:"latencyMs"`
}

type RingBuffer struct {
	mu      sync.RWMutex
	records []QueryRecord
	head    int
	full    bool
	size    int
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		records: make([]QueryRecord, size),
		size:    size,
	}
}

func (rb *RingBuffer) Push(r QueryRecord) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.records[rb.head] = r
	rb.head = (rb.head + 1) % rb.size
	if rb.head == 0 {
		rb.full = true
	}
}

func (rb *RingBuffer) Snapshot() []QueryRecord {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if !rb.full {
		res := make([]QueryRecord, rb.head)
		copy(res, rb.records[:rb.head])
		return res
	}

	res := make([]QueryRecord, rb.size)
	copy(res, rb.records[rb.head:])
	copy(res[rb.size-rb.head:], rb.records[:rb.head])
	return res
}
