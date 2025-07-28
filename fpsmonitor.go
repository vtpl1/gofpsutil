package fpsmonitor

import (
	"fmt"
	"log"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

const logInterval = 10

type AtomicFloat64 uint64

func (m *AtomicFloat64) Load() float64 {
	return math.Float64frombits(atomic.LoadUint64((*uint64)(m)))
}

func (m *AtomicFloat64) Store(val float64) {
	atomic.StoreUint64((*uint64)(m), math.Float64bits(val))
}

func (m *AtomicFloat64) Add(delta float64) float64 {
	for {
		oldData := m.Load()
		newData := oldData + delta

		if atomic.CompareAndSwapUint64((*uint64)(m), math.Float64bits(oldData), math.Float64bits(newData)) {
			return newData
		}
	}
}

type FpsMonitor interface {
	Register(appID, chID, threadID uint64)
	Add(appID, chID, threadID, count uint64)
	Load(appID, chID, threadID uint64) (fps float64)
	Shutdown()
}

type fpsKey struct {
	AppID, ChID, ThreadID uint64
}

// String implements fmt.Stringer.
func (m fpsKey) String() string {
	return fmt.Sprintf("%04d.%04d.%04d", m.AppID, m.ChID, m.ThreadID)
}

type status struct {
	count   atomic.Uint64
	lastFps AtomicFloat64
}

type fpsMonitorImpl struct {
	muStats sync.RWMutex
	stats   map[string]*status
	wg      sync.WaitGroup
	quit    chan struct{}
	logger  *log.Logger
}

// Add implements FpsMonitor.
func (m *fpsMonitorImpl) Add(appID uint64, chID uint64, threadID uint64, count uint64) {
	key := fpsKey{appID, chID, threadID}.String()

	m.muStats.RLock()
	s, ok := m.stats[key]
	m.muStats.RUnlock()

	if ok {
		s.count.Add(count)

		return
	}

	m.Register(appID, chID, threadID)
}

// Load implements FpsMonitor.
func (m *fpsMonitorImpl) Load(appID uint64, chID uint64, threadID uint64) float64 {
	key := fpsKey{appID, chID, threadID}.String()

	m.muStats.RLock()
	s, ok := m.stats[key]
	m.muStats.RUnlock()

	if ok {
		return s.lastFps.Load()
	}

	return 0.0
}

// Register implements FpsMonitor.
func (m *fpsMonitorImpl) Register(appID uint64, chID uint64, threadID uint64) {
	key := fpsKey{appID, chID, threadID}.String()

	m.muStats.Lock()
	defer m.muStats.Unlock()

	if _, ok := m.stats[key]; !ok {
		m.stats[key] = &status{ //nolint:exhaustruct
			lastFps: 0.0,
		}
	}
}

// Shutdown implements FpsMonitor.
func (m *fpsMonitorImpl) Shutdown() {
	stopOnce.Do(func() {
		close(m.quit)
		m.wg.Wait()
	})
}

var (
	monitorInstance FpsMonitor //nolint:gochecknoglobals
	startOnce       sync.Once  //nolint:gochecknoglobals
	stopOnce        sync.Once  //nolint:gochecknoglobals
)

// GetFpsMonitor returns the singleton instance.
//
//nolint:ireturn
func GetFpsMonitor(dir, suffix string) FpsMonitor {
	startOnce.Do(func() {
		logPath := filepath.Join(dir, "fps_"+suffix+".log")
		monitorInstance = newFpsMonitorImpl(logPath)
	})

	return monitorInstance
}

func newFpsMonitorImpl(logPath string) *fpsMonitorImpl {
	m := &fpsMonitorImpl{
		muStats: sync.RWMutex{},
		stats:   make(map[string]*status),
		wg:      sync.WaitGroup{},
		quit:    make(chan struct{}),
		logger: log.New(&lumberjack.Logger{ //nolint:exhaustruct
			Filename:   logPath,
			MaxSize:    5, //nolint:mnd
			MaxBackups: 3, //nolint:mnd
			MaxAge:     0,
			Compress:   false,
		}, "", 0),
	}

	m.wg.Add(1)

	go m.run()

	return m
}

func (m *fpsMonitorImpl) run() {
	ticker := time.NewTicker(logInterval * time.Second)
	defer func() {
		ticker.Stop()
		m.wg.Done()
	}()

	lastTime := time.Now()
	count := 0

	for {
		select {
		case <-ticker.C:
			lastTime = m.dump(lastTime, &count)
			count--
		case <-m.quit:
			return
		}
	}
}

func (m *fpsMonitorImpl) dumpHeader(now time.Time, keys []string) string {
	var builder strings.Builder
	for range keys {
		builder.WriteString("Time     App .Chn .Thr       Fps|")
	}

	builder.WriteByte('\n')

	for _, key := range keys {
		builder.WriteString(fmt.Sprintf("%s %s      fps|", now.Format("06-01-02"), key))
	}

	builder.WriteByte('\n')

	return builder.String()
}

func (m *fpsMonitorImpl) dump(lastTime time.Time, count *int) time.Time {
	now := time.Now()
	diff := now.Sub(lastTime)

	m.muStats.RLock()

	keys := make([]string, 0, len(m.stats))
	for k := range m.stats {
		keys = append(keys, k)
	}
	m.muStats.RUnlock()
	sort.Strings(keys)

	var builder strings.Builder

	if *count == 0 {
		*count = 100

		builder.WriteString(m.dumpHeader(now, keys))
	}

	for _, key := range keys {
		m.muStats.RLock()
		s := m.stats[key]
		m.muStats.RUnlock()

		current := s.count.Swap(0)
		fps := float64(current) * 1000.0 / float64(diff.Milliseconds()) //nolint:mnd
		s.lastFps.Store(fps)
		builder.WriteString(fmt.Sprintf("%s %s %8.1f|", now.Format("15:04:05"), key, fps))
	}

	m.logger.Println(builder.String())

	return now
}
