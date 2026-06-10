package agent

// historyID generator: a dedicated goroutine owns a monotonically
// incrementing counter and serves requests via two channels.
// This avoids data races between the task goroutine and tool goroutines
// without locks or atomics on the counter itself.

// newHistoryIDGenerator starts a goroutine that owns a counter.
// Returns send-only channels: inc adds N, get replies with the current value.
func newHistoryIDGenerator() (inc chan<- uint64, get chan<- chan<- uint64) {
	incCh := make(chan uint64)
	getCh := make(chan chan<- uint64)
	go func() {
		var h uint64
		for {
			select {
			case n := <-incCh:
				h += n
			case reply := <-getCh:
				reply <- h
			}
		}
	}()
	return incCh, getCh
}

// histInc adds n to the history counter.
func (s *Session) histInc(n uint64) {
	s.histIncCh <- n
}

// histGet returns the current value of the history counter.
func (s *Session) histGet() uint64 {
	reply := make(chan uint64, 1)
	s.histGetCh <- reply
	return <-reply
}

// histIncAndGet increments the counter by 1 and returns the new value.
func (s *Session) histIncAndGet() uint64 {
	reply := make(chan uint64, 1)
	s.histIncCh <- 1
	s.histGetCh <- reply
	return <-reply
}

// histSyncAfterLoad advances the history counter past the highest Content ID
// so that new streaming IDs don't collide with IDs from the loaded session.
func (s *Session) histSyncAfterLoad() {
	var maxID uint64
	for _, item := range s.Content {
		if item.ID > maxID {
			maxID = item.ID
		}
	}
	if maxID > 0 {
		s.histInc(maxID)
	}
}
