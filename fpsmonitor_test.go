package fpsmonitor_test

import (
	"os"
	"testing"
	"time"

	fpsmonitor "github.com/vtpl1/gofpsutil"
)

func TestFpsMonitor_PublicAPI_Behavior(t *testing.T) {
	// Get singleton instance
	dir, _ := os.Getwd()
	prefix := "public_api_test"
	monitor := fpsmonitor.GetFpsMonitor(dir, prefix)
	defer monitor.Shutdown()
	// Simulate FPS updates
	for i := 0; i < 15000; i++ {
		monitor.Add(1, 2, 3, 1)
		monitor.Add(4, 5, 6, 1)
		monitor.Add(1, 5, 6, 1)
		// Wait for internal writer to flush (1s interval)
		time.Sleep(time.Millisecond)
	}

	monitor.Shutdown()
	monitor.Shutdown() // should be safe to call twice
}
