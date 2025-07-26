package fpsmonitor

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	logIntervalSec       = 10
	maxValidListSize     = 100
	minAbnormalFPS       = 8
	maxAbnormalFPS       = 1000
	bannerWidth          = 80
	strftimeFormatLength = 20
)

type FpsStatus struct {
	AppID     uint64
	ChannelID uint64
	ThreadID  uint64

	Value     atomic.Uint64
	LastValue atomic.Uint64
	LastFPS   atomic.Value // float64
	DumpInLog bool
}

type FpsMonitor struct {
	sessionDir    string
	fileName      string
	lastWriteTs   int64
	doShutdown    atomic.Bool
	doWriteHead   atomic.Bool
	rawLogger     *log.Logger
	summaryLogger *log.Logger
	resourceMap   sync.Map // key: tuple -> *FpsStatus
	shutdownOnce  sync.Once
}

var singleton *FpsMonitor
var once sync.Once

func GetInstance(sessionDir, fileName string) *FpsMonitor {
	once.Do(func() {
		singleton = &FpsMonitor{
			sessionDir:  sessionDir,
			fileName:    fileName,
			lastWriteTs: time.Now().UnixMilli(),
		}
		singleton.rawLogger = newRawRotatingLogger(filepath.Join(sessionDir, fileName+".log"))
		singleton.summaryLogger = newRawRotatingLogger(filepath.Join(sessionDir, fileName+"_summary.log"))
		singleton.doWriteHead.Store(true)
		go singleton.run()
	})
	return singleton
}

func newRawRotatingLogger(logPath string) *log.Logger {
	writer := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    5,
		MaxBackups: 3,
		MaxAge:     28,
		Compress:   true,
	}
	return log.New(writer, "", 0)
}

func (m *FpsMonitor) SetStatus(appID, channelID, threadID uint64, dump bool) *atomic.Uint64 {
	key := fmt.Sprintf("%d-%d-%d", appID, channelID, threadID)
	val, loaded := m.resourceMap.Load(key)
	if loaded {
		status := val.(*FpsStatus)
		status.DumpInLog = dump
		status.Value.Add(1)
		return &status.Value
	}

	status := &FpsStatus{
		AppID:     appID,
		ChannelID: channelID,
		ThreadID:  threadID,
		DumpInLog: dump,
	}
	status.LastFPS.Store(float64(0))
	status.Value.Add(1)
	m.resourceMap.Store(key, status)
	return &status.Value
}

func (m *FpsMonitor) run() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastSize int
	for !m.doShutdown.Load() {
		select {
		case <-ticker.C:
			currentSize := 0
			m.resourceMap.Range(func(_, _ any) bool {
				currentSize++
				return true
			})
			if currentSize != lastSize {
				m.doWriteHead.Store(true)
				lastSize = currentSize
			}

			if time.Now().Unix()%logIntervalSec == 0 {
				m.writeData()
			}
		}
	}
	m.writeData()
}

func (m *FpsMonitor) writeData() {
	m.calculateFPS()
	if m.doWriteHead.Swap(false) {
		m.writeHeader()
	}

	var validList, invalidList []string
	now := time.Now().Format("15:04:05")

	var data strings.Builder
	m.resourceMap.Range(func(key, value any) bool {
		status := value.(*FpsStatus)
		if !status.DumpInLog {
			return true
		}
		fps := status.LastFPS.Load().(float64)
		data.WriteString(fmt.Sprintf("%s %04d.%04d.%04d %9.1f|\n", now, status.AppID, status.ChannelID, status.ThreadID, fps))
		if fps >= minAbnormalFPS && fps <= maxAbnormalFPS {
			validList = append(validList, key.(string))
		} else {
			invalidList = append(invalidList, key.(string))
		}
		return true
	})

	if data.Len() > 0 {
		m.rawLogger.Print(data.String())
	}

	m.summaryLogger.Printf("%s Valid   (x2) (%04d): %s", now, len(validList), strings.Join(validList, " "))
	m.summaryLogger.Printf("%s Invalid (x2) (%04d): %s", now, len(invalidList), strings.Join(invalidList, " "))
	m.summaryLogger.Printf("%s --------------", now)
}

func (m *FpsMonitor) calculateFPS() {
	now := time.Now().UnixMilli()
	diff := now - m.lastWriteTs
	if diff <= 0 {
		return
	}
	m.resourceMap.Range(func(_, value any) bool {
		status := value.(*FpsStatus)
		delta := status.Value.Load() - status.LastValue.Load()
		fps := float64(delta) * 1000.0 / float64(diff)
		status.LastFPS.Store(fps)
		status.LastValue.Store(status.Value.Load())
		return true
	})
	m.lastWriteTs = now
}

func (m *FpsMonitor) writeHeader() {
	now := time.Now().Format("2006-01-02 15:04:05")
	banner := fmt.Sprintf("\n┌%s┐\n│%s│\n└%s┘\n",
		strings.Repeat("─", bannerWidth),
		centerText("UTC: "+now, bannerWidth),
		strings.Repeat("─", bannerWidth),
	)
	m.rawLogger.Print(banner)

	var hdr strings.Builder
	count := 0
	m.resourceMap.Range(func(_, value any) bool {
		status := value.(*FpsStatus)
		if status.DumpInLog {
			hdr.WriteString(fmt.Sprintf("Time     %04d.%04d.%04d        Fps|", status.AppID, status.ChannelID, status.ThreadID))
		}
		count++
		return true
	})

	if hdr.Len() > 0 {
		m.rawLogger.Println(hdr.String())
	}
	m.summaryLogger.Printf("Total channels (x2) %d", count)
}

func countMap(m *sync.Map) int {
	count := 0
	m.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func centerText(s string, width int) string {
	if len(s) >= width {
		return s
	}
	pad := (width - len(s)) / 2
	return strings.Repeat(" ", pad) + s + strings.Repeat(" ", width-len(s)-pad)
}

func (m *FpsMonitor) Shutdown() {
	m.shutdownOnce.Do(func() {
		m.doShutdown.Store(true)
	})
}

func SetStatus(appID, channelID, threadID uint64) {
	GetInstance("session", "fps_common").SetStatus(appID, channelID, threadID, true)
}
