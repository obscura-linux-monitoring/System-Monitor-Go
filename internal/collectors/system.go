package collectors

import (
	"sync"
	"system-monitor/internal/logger"
	"system-monitor/internal/models"
	"time"

	"system-monitor/internal/config"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/process"
)

// SystemCollector는 기본 시스템 정보를 수집합니다.
type SystemCollector struct {
	cachedInfo        models.SystemInfo
	cachedTime        time.Time
	cacheDuration     time.Duration
	updateFrequency   time.Duration
	mutex             sync.RWMutex
	backgroundRunning bool
	staticInfoLoaded  bool // 고정 정보가 이미 로드되었는지 여부
}

// NewSystemCollector는 새로운 SystemCollector를 초기화하고 백그라운드 수집을 시작합니다
func NewSystemCollector(cfg config.SystemCollectorSettings) *SystemCollector {
	collector := &SystemCollector{
		cacheDuration:    time.Duration(cfg.CacheDurationSeconds) * time.Second,
		updateFrequency:  time.Duration(cfg.UpdateFrequencySeconds) * time.Second,
		staticInfoLoaded: false,
	}
	collector.startBackgroundCollection()
	return collector
}

// 백그라운드 수집 함수
func (c *SystemCollector) startBackgroundCollection() {
	if c.backgroundRunning {
		return
	}

	c.backgroundRunning = true
	go func() {
		for {
			info, err := c.collectSystemInfo()
			if err == nil {
				c.mutex.Lock()
				c.cachedInfo = info
				c.cachedTime = time.Now()
				c.mutex.Unlock()
			}
			time.Sleep(c.updateFrequency)
		}
	}()
}

// 고정 정보만 수집하는 함수
func (c *SystemCollector) collectStaticInfo() (models.SystemInfo, error) {
	var info models.SystemInfo

	// 호스트 정보 가져오기
	infoStat, err := host.Info()
	if err != nil {
		return info, err
	}

	// 커널 버전 가져오기
	kernelVersion, err := host.KernelVersion()
	if err != nil {
		return info, err
	}

	// 고정값 설정
	info.Hostname = infoStat.Hostname
	info.OSName = infoStat.OS
	info.PlatformFamily = infoStat.PlatformFamily
	info.Platform = infoStat.Platform
	info.OSVersion = infoStat.PlatformVersion
	info.OSArchitecture = infoStat.KernelArch
	info.OSKernelVersion = kernelVersion

	return info, nil
}

// 변동 정보만 수집하는 함수
func (c *SystemCollector) collectDynamicInfo(info *models.SystemInfo) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var hostInfoErr, processInfoErr error

	wg.Add(2)

	// 호스트 동적 정보 수집 (부팅 시간, 업타임)
	go func() {
		defer wg.Done()

		infoStat, err := host.Info()
		if err != nil {
			hostInfoErr = err
			return
		}

		mu.Lock()
		info.BootTime = int64(infoStat.BootTime)
		info.Uptime = int64(infoStat.Uptime)
		mu.Unlock()
	}()

	// 프로세스 관련 정보 수집
	go func() {
		defer wg.Done()

		processes, err := process.Processes()
		if err != nil {
			processInfoErr = err
			return
		}

		var totalProcesses uint64
		var totalThreads uint64
		var totalFileDescriptors uint64

		for _, p := range processes {
			totalProcesses++

			numThreads, err := p.NumThreads()
			if err == nil {
				totalThreads += uint64(numThreads)
			}

			fds, err := p.NumFDs()
			if err == nil {
				totalFileDescriptors += uint64(fds)
			}
		}

		mu.Lock()
		info.TotalProcesses = totalProcesses
		info.TotalThreads = totalThreads
		info.TotalFileDescriptors = totalFileDescriptors
		mu.Unlock()
	}()

	wg.Wait()

	if hostInfoErr != nil {
		return hostInfoErr
	}
	if processInfoErr != nil {
		return processInfoErr
	}

	return nil
}

// collectSystemInfo는 실제로 시스템 정보를 수집하는 내부 메서드입니다
func (c *SystemCollector) collectSystemInfo() (models.SystemInfo, error) {
	var info models.SystemInfo

	// 고정값 로드 여부 확인
	c.mutex.RLock()
	isStaticInfoLoaded := c.staticInfoLoaded
	if isStaticInfoLoaded {
		// 기존 캐시에서 고정값 복사
		info = c.cachedInfo
	}
	c.mutex.RUnlock()

	// 고정값이 없으면 고정값 수집
	if !isStaticInfoLoaded {
		logger.Info("수집: 시스템 고정 정보")
		var err error
		info, err = c.collectStaticInfo()
		if err != nil {
			return info, err
		}

		// 고정값 로드 완료 표시
		c.mutex.Lock()
		c.staticInfoLoaded = true
		c.mutex.Unlock()
	}

	// 변동값 수집
	logger.Info("수집: 시스템 변동 정보")
	err := c.collectDynamicInfo(&info)
	if err != nil {
		return info, err
	}

	return info, nil
}

func (c *SystemCollector) Collect() (interface{}, error) {
	logger.Info("SystemCollector Collect")
	start := time.Now()

	// 캐시된 시스템 정보 확인
	c.mutex.RLock()
	isCacheValid := time.Since(c.cachedTime) < c.cacheDuration
	cachedInfo := c.cachedInfo
	c.mutex.RUnlock()

	// 캐시가 유효하면 캐시된 정보 반환
	if isCacheValid {
		logger.Info("시스템 정보 캐시에서 반환")
		return cachedInfo, nil
	}

	// 캐시가 유효하지 않으면 직접 수집
	logger.Info("시스템 정보를 직접 수집합니다")
	info, err := c.collectSystemInfo()
	if err != nil {
		return nil, err
	}

	// 수집한 정보 캐시에 저장
	c.mutex.Lock()
	c.cachedInfo = info
	c.cachedTime = time.Now()
	c.mutex.Unlock()

	logger.Info("SystemCollector Collect End")
	logger.Info("Duration: " + time.Since(start).String())
	return info, nil
}
