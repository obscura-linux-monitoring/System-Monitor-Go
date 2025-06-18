package collectors

import (
	"os/user"
	"strconv"
	"sync"
	"system-monitor/internal/config"
	"system-monitor/internal/logger"
	"system-monitor/internal/models"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// ProcessCollector는 프로세스 정보를 수집합니다.
type ProcessCollector struct {
	cachedMetrics     []models.ProcessInfo
	cachedTime        time.Time
	cacheDuration     time.Duration
	updateFrequency   time.Duration
	mutex             sync.RWMutex
	backgroundRunning bool
	prevProcesses     map[int32]*process.Process
	prevCPUTimes      map[int32]float64
	prevTime          time.Time
}

// NewProcessCollector는 새로운 ProcessCollector를 초기화하고 백그라운드 수집을 시작합니다.
func NewProcessCollector(cfg config.ProcessCollectorSettings) *ProcessCollector {
	collector := &ProcessCollector{
		cacheDuration:   time.Duration(cfg.CacheDurationSeconds) * time.Second,
		updateFrequency: time.Duration(cfg.UpdateFrequencySeconds) * time.Second,
		prevProcesses:   make(map[int32]*process.Process),
		prevCPUTimes:    make(map[int32]float64),
	}
	collector.startBackgroundCollection()
	return collector
}

func (c *ProcessCollector) startBackgroundCollection() {
	if c.backgroundRunning {
		return
	}
	c.backgroundRunning = true

	go func() {
		// 첫 수집을 즉시 실행하여 초기 데이터를 확보합니다.
		c.collectAndCache()

		ticker := time.NewTicker(c.updateFrequency)
		defer ticker.Stop()

		for range ticker.C {
			c.collectAndCache()
		}
	}()
}

func (c *ProcessCollector) collectAndCache() {
	metrics, err := c.collectProcessMetrics()
	if err == nil {
		c.mutex.Lock()
		c.cachedMetrics = metrics
		c.cachedTime = time.Now()
		c.mutex.Unlock()
	} else {
		logger.Error("백그라운드 프로세스 메트릭 수집 실패: " + err.Error())
	}
}

// getUsernameFromUID는 사용자 ID에서 사용자 이름을 조회합니다.
func getUsernameFromUID(uid string) string {
	u, err := user.LookupId(uid)
	if err != nil {
		return uid
	}
	return u.Username
}

func (c *ProcessCollector) collectProcessMetrics() ([]models.ProcessInfo, error) {
	processes, err := process.Processes()
	if err != nil {
		return nil, err
	}

	// 현재 시간 기록
	currentTime := time.Now()

	// 이전 측정 시간과의 차이 계산 (초 단위)
	var duration float64
	isFirstCollection := c.prevTime.IsZero()
	if !isFirstCollection {
		duration = currentTime.Sub(c.prevTime).Seconds()
	}

	if duration <= 0 {
		duration = 1.0
	}

	var metrics []models.ProcessInfo
	var newProcesses = make(map[int32]*process.Process)
	var newCPUTimes = make(map[int32]float64)

	for _, p := range processes {
		pid := p.Pid
		newProcesses[pid] = p

		var processInfo models.ProcessInfo
		processInfo.PID = int(pid)

		// PID 설정
		processInfo.PID = int(pid)

		// PPID(부모 프로세스 ID) 설정
		if ppid, err := p.Ppid(); err == nil {
			processInfo.PPID = int(ppid)
		}

		// 프로세스 이름 설정
		if name, err := p.Name(); err == nil {
			processInfo.Name = name
		}

		// 명령어 설정
		if cmdline, err := p.Cmdline(); err == nil {
			processInfo.Command = cmdline
		}

		// 사용자 설정
		if uids, err := p.Uids(); err == nil && len(uids) > 0 {
			processInfo.User = getUsernameFromUID(strconv.Itoa(int(uids[0])))
		}

		// 상태 설정
		if status, err := p.Status(); err == nil && len(status) > 0 {
			processInfo.Status = status[0]
		}

		// CPU 시간 계산
		cpuTime := 0.0
		if times, err := p.Times(); err == nil {
			cpuTime = times.Total()
			newCPUTimes[pid] = cpuTime
		}
		processInfo.CPUTime = cpuTime

		// CPU 사용률 계산
		if !isFirstCollection {
			if prevTime, ok := c.prevCPUTimes[pid]; ok {
				cpuUsage := (cpuTime - prevTime) / duration * 100.0
				if cpuUsage >= 0 {
					processInfo.CPUUsage = cpuUsage
				}
			}
		}

		// 메모리 RSS 설정
		if memInfo, err := p.MemoryInfo(); err == nil && memInfo != nil {
			processInfo.MemoryRSS = int64(memInfo.RSS)
			processInfo.MemoryVSZ = int64(memInfo.VMS)
		}

		// Nice 값 설정
		if nice, err := p.Nice(); err == nil {
			processInfo.Nice = int(nice)
		}

		// 스레드 수 설정
		if numThreads, err := p.NumThreads(); err == nil {
			processInfo.Threads = int(numThreads)
		}

		// 오픈 파일 수 설정
		if openFiles, err := p.OpenFiles(); err == nil {
			processInfo.OpenFiles = len(openFiles)
		}

		// 시작 시간 설정
		if createTime, err := p.CreateTime(); err == nil {
			processInfo.StartTime = createTime / 1000 // 밀리초를 초로 변환
		}

		// IO 통계 설정
		if ioCounters, err := p.IOCounters(); err == nil && ioCounters != nil {
			processInfo.IOReadBytes = int64(ioCounters.ReadBytes)
			processInfo.IOWriteBytes = int64(ioCounters.WriteBytes)
		}

		metrics = append(metrics, processInfo)
	}

	// 현재 상태를 저장하여 다음번 수집에 사용합니다.
	c.mutex.Lock()
	c.prevProcesses = newProcesses
	c.prevCPUTimes = newCPUTimes
	c.prevTime = currentTime
	c.mutex.Unlock()

	return metrics, nil
}

func (c *ProcessCollector) Collect() (interface{}, error) {
	logger.Info("ProcessCollector Collect Start")
	start := time.Now()

	c.mutex.RLock()
	isCacheValid := time.Since(c.cachedTime) < c.cacheDuration && len(c.cachedMetrics) > 0
	cachedMetrics := c.cachedMetrics
	c.mutex.RUnlock()

	if isCacheValid {
		logger.Info("프로세스 정보 캐시에서 반환")
		return cachedMetrics, nil
	}

	logger.Info("프로세스 정보를 직접 수집합니다")
	metrics, err := c.collectProcessMetrics()
	if err != nil {
		return nil, err
	}

	c.mutex.Lock()
	c.cachedMetrics = metrics
	c.cachedTime = time.Now()
	c.mutex.Unlock()

	logger.Info("ProcessCollector Collect End")
	logger.Info("Duration: " + time.Since(start).String())
	return metrics, nil
}
