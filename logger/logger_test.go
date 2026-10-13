package logger

import (
	"sync"
	"testing"
)

// Concurrent Telegram delayed deletes write logs while the panel reads them.
func TestConcurrentBufferWritesAndReads(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				addToBuffer("INFO", "telegram delayed delete")
				GetLogs(10, "INFO")
			}
		}()
	}
	wg.Wait()
	if len(GetLogs(10, "INFO")) == 0 {
		t.Fatal("logs lost")
	}
}
