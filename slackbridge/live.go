package main

import "sync"

// live returns a progress func that keeps one status message in the thread: the first call
// posts it, later calls edit it, so the thread shows the run's log as it grows.
func (b *Bridge) live(channel, thread string) func(string) {
	var mu sync.Mutex
	var ts, last string
	return func(text string) {
		mu.Lock()
		defer mu.Unlock()
		if text == last {
			return
		}
		last = text
		if ts == "" {
			ts = b.out.Post(channel, thread, text)
			return
		}
		b.out.Update(channel, ts, text)
	}
}
