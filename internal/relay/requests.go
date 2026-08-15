package relay

import (
	"sync"
	"time"

	"modelrelay/internal/store"
)

// requestRecord 是单次请求的摘要记录（不含正文）。
type requestRecord struct {
	RequestID  string
	Path       string
	Model      string
	Node       string
	Status     int
	TTFTMs     int64
	DurationMs int64
	ErrorCode  string
	Time       time.Time
}

// ringBuffer 是请求记录的有界内存环。
type ringBuffer struct {
	mu    sync.Mutex
	items []requestRecord
	max   int
}

func newRingBuffer(max int) *ringBuffer {
	if max <= 0 {
		max = 500
	}
	return &ringBuffer{max: max}
}

func (r *ringBuffer) push(item requestRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, item)
	if len(r.items) > r.max {
		r.items = r.items[len(r.items)-r.max:]
	}
}

func (r *ringBuffer) list() []requestRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]requestRecord, len(r.items))
	copy(out, r.items)
	return out
}

// summaryWriter 异步将请求摘要写入 SQLite，避免阻塞数据路径。
type summaryWriter struct {
	ch   chan store.RequestSummary
	stop chan struct{}
	done chan struct{}
}

func newSummaryWriter() *summaryWriter {
	return &summaryWriter{
		ch:   make(chan store.RequestSummary, 256),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

func (w *summaryWriter) start(st *store.Store) {
	go func() {
		defer close(w.done)
		for {
			select {
			case <-w.stop:
				// 排空剩余。
				for {
					select {
					case s := <-w.ch:
						_ = st.AddRequestSummary(s)
					default:
						return
					}
				}
			case s := <-w.ch:
				_ = st.AddRequestSummary(s)
			}
		}
	}()
}

func (w *summaryWriter) submit(s store.RequestSummary) {
	select {
	case w.ch <- s:
	default: // 队列满则丢弃（观测不阻塞业务）。
	}
}

func (w *summaryWriter) shutdown() {
	close(w.stop)
	<-w.done
}
