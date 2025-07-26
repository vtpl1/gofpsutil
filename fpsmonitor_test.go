package fpsmonitor

import (
	"os"
	"testing"
	"time"
)

func TestFpsMonitor_PublicAPI_Behavior(t *testing.T) {
	// Get singleton instance
	dir, _ := os.Getwd()
	prefix := "public_api_test"
	monitor := GetInstance(dir, prefix)

	// Set counters
	counterValid := monitor.SetStatus(1, 2, 3, true)
	counterInvalid := monitor.SetStatus(4, 5, 6, true)

	// Simulate FPS updates
	for i := 0; i < 20000; i++ {
		(*counterValid).Add(1)
		(*counterInvalid).Add(1)
		// Wait for internal writer to flush (1s interval)
		time.Sleep(time.Millisecond)
	}

	monitor.Shutdown()
	monitor.Shutdown() // should be safe to call twice
}
