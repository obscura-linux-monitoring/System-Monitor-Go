package collectors

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"system-monitor/internal/logger"
	"system-monitor/internal/models"
	"time"

	"system-monitor/internal/config"

	"github.com/jaypipes/ghw"
	"github.com/shirou/gopsutil/v3/mem"
)

// MemoryCollector는 메모리 메트릭을 수집합니다.
type MemoryCollector struct {
	cachedInfo        models.MemoryMetrics
	cachedTime        time.Time
	cacheDuration     time.Duration
	updateFrequency   time.Duration
	mutex             sync.RWMutex
	backgroundRunning bool
	staticInfoLoaded  bool // 고정 정보가 이미 로드되었는지 여부
	collectStaticOnce sync.Once
}

// NewMemoryCollector는 새로운 MemoryCollector를 초기화하고 백그라운드 수집을 시작합니다
func NewMemoryCollector(cfg config.MemoryCollectorSettings) *MemoryCollector {
	collector := &MemoryCollector{
		cacheDuration:    time.Duration(cfg.CacheDurationSeconds) * time.Second,
		updateFrequency:  time.Duration(cfg.UpdateFrequencySeconds) * time.Second,
		staticInfoLoaded: false,
	}
	collector.startBackgroundCollection()
	return collector
}

// 백그라운드 수집 함수
func (c *MemoryCollector) startBackgroundCollection() {
	if c.backgroundRunning {
		return
	}

	c.backgroundRunning = true
	go func() {
		for {
			info, err := c.collectMemoryInfo()
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

// 고정 정보만 수집하는 함수 (메모리 총량, 하드웨어 정보 등)
func (c *MemoryCollector) collectStaticInfo() (models.MemoryMetrics, error) {
	var metrics models.MemoryMetrics

	// 메모리 정보 가져오기
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return metrics, err
	}

	// 스왑 정보 가져오기
	swapInfo, err := mem.SwapMemory()
	if err != nil {
		return metrics, err
	}

	// 고정값 설정
	metrics.Total = int64(memInfo.Total)
	metrics.SwapTotal = int64(swapInfo.Total)

	// 메모리 하드웨어 정보 설정
	err = c.getMemoryHardwareInfo(&metrics)
	if err != nil {
		logger.Error("메모리 하드웨어 정보 수집 실패: " + err.Error())
		// 기본값 설정
		metrics.DataRate = 0
		metrics.UsingSlotCount = 0
		metrics.TotalSlotCount = 0
		metrics.FormFactor = ""
	}

	return metrics, nil
}

// 변동 정보만 수집하는 함수 (메모리 사용량, 버퍼, 캐시 등)
func (c *MemoryCollector) collectDynamicInfo(metrics *models.MemoryMetrics) error {
	// 메모리 사용량 정보 가져오기
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return err
	}

	// 스왑 정보 가져오기
	swapInfo, err := mem.SwapMemory()
	if err != nil {
		return err
	}

	// 변동값 설정
	metrics.Used = int64(memInfo.Used)
	metrics.Free = int64(memInfo.Free)
	metrics.Available = int64(memInfo.Available)
	metrics.Buffers = int64(memInfo.Buffers)
	metrics.Cached = int64(memInfo.Cached)
	metrics.UsagePercent = memInfo.UsedPercent

	// 스왑 사용량
	metrics.SwapUsed = int64(swapInfo.Used)
	metrics.SwapFree = int64(swapInfo.Free)

	// Linux에서는 항상 0으로 설정 (Windows 전용 필드)
	metrics.PagedPoolSize = 0
	metrics.NonPagedPoolSize = 0

	return nil
}

// collectMemoryInfo는 실제로 메모리 정보를 수집하는 내부 메서드입니다
func (c *MemoryCollector) collectMemoryInfo() (models.MemoryMetrics, error) {
	var staticErr error
	var metrics models.MemoryMetrics

	// collectStaticInfo를 한 번만 실행하도록 보장
	c.collectStaticOnce.Do(func() {
		logger.Info("수집: 메모리 고정 정보")
		staticMetrics, err := c.collectStaticInfo()
		if err != nil {
			staticErr = err
			return
		}

		c.mutex.Lock()
		// Copy static fields to cachedInfo
		c.cachedInfo.Total = staticMetrics.Total
		c.cachedInfo.SwapTotal = staticMetrics.SwapTotal
		c.cachedInfo.DataRate = staticMetrics.DataRate
		c.cachedInfo.UsingSlotCount = staticMetrics.UsingSlotCount
		c.cachedInfo.TotalSlotCount = staticMetrics.TotalSlotCount
		c.cachedInfo.FormFactor = staticMetrics.FormFactor
		c.staticInfoLoaded = true
		c.mutex.Unlock()
	})

	if staticErr != nil {
		return models.MemoryMetrics{}, staticErr
	}

	c.mutex.RLock()
	// 이제 고정 정보는 c.cachedInfo에 있음. 복사해서 사용.
	metrics = c.cachedInfo
	c.mutex.RUnlock()

	// 변동값 수집
	logger.Info("수집: 메모리 변동 정보")
	err := c.collectDynamicInfo(&metrics)
	if err != nil {
		return metrics, err
	}

	return metrics, nil
}

// Collect 함수 수정 - 중복 호출 방지
func (c *MemoryCollector) Collect() (interface{}, error) {
	logger.Info("MemoryCollector Collect")
	start := time.Now()

	// 캐시된 메모리 정보 확인
	c.mutex.RLock()
	isCacheValid := time.Since(c.cachedTime) < c.cacheDuration
	isStaticLoaded := c.staticInfoLoaded
	cachedInfo := c.cachedInfo
	c.mutex.RUnlock()

	// 캐시가 유효하고 고정 정보가 로드되었으면 캐시된 정보 반환
	if isCacheValid && isStaticLoaded {
		logger.Info("메모리 정보 캐시에서 반환")
		return cachedInfo, nil
	}

	// 새로운 메모리 정보 수집 (직접 또는 백그라운드 갱신 실패한 경우)
	info, err := c.collectMemoryInfo()
	if err != nil {
		return nil, err
	}

	// 수집한 정보 캐시에 저장
	c.mutex.Lock()
	c.cachedInfo = info
	c.cachedTime = time.Now()
	c.mutex.Unlock()

	logger.Info("MemoryCollector Collect End")
	logger.Info("Duration: " + time.Since(start).String())
	return info, nil
}

// getMemoryHardwareInfo는 메모리 하드웨어 정보를 수집하는 함수입니다.
// Linux에서 ghw 라이브러리와 dmidecode 명령을 조합하여 구현합니다.
func (c *MemoryCollector) getMemoryHardwareInfo(metrics *models.MemoryMetrics) error {
	// dmidecode 명령을 사용하여 메모리 정보 수집
	cmd := exec.Command("sudo", "dmidecode", "-t", "memory")
	output, err := cmd.Output()
	if err != nil {
		logger.Error("dmidecode 명령 실행 실패, ghw로 대체: " + err.Error())
		// ghw를 대체 수단으로 사용
		memory, ghwErr := ghw.Memory()
		if ghwErr != nil {
			return ghwErr // 둘 다 실패하면 에러 반환
		}
		if len(memory.Modules) > 0 {
			usedSlots := 0
			for _, module := range memory.Modules {
				if module.SizeBytes > 0 {
					usedSlots++
				}
			}
			metrics.UsingSlotCount = uint16(usedSlots)
			metrics.TotalSlotCount = uint16(len(memory.Modules))
		}
		return nil
	}

	lines := strings.Split(string(output), "\n")
	var totalSlots, usedSlots uint16
	var inMemoryDevice bool

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "Memory Device") {
			inMemoryDevice = true
			totalSlots++
		}

		if inMemoryDevice {
			if strings.HasPrefix(line, "Size:") {
				if !strings.Contains(line, "No Module Installed") {
					usedSlots++
				}
			}

			if strings.HasPrefix(line, "Speed:") && metrics.DataRate == 0 {
				speedInfo := strings.TrimSpace(strings.TrimPrefix(line, "Speed:"))
				if strings.Contains(speedInfo, "MT/s") {
					speedStr := strings.Split(speedInfo, " ")[0]
					if speed, err := strconv.ParseUint(speedStr, 10, 64); err == nil {
						metrics.DataRate = speed
					}
				}
			}

			if strings.HasPrefix(line, "Form Factor:") && metrics.FormFactor == "" {
				formFactor := strings.TrimSpace(strings.TrimPrefix(line, "Form Factor:"))
				if formFactor != "Unknown" {
					metrics.FormFactor = formFactor
				}
			}
		}

		// 빈 줄은 장치 섹션의 끝을 의미할 수 있음
		if line == "" {
			inMemoryDevice = false
		}
	}

	metrics.TotalSlotCount = totalSlots
	metrics.UsingSlotCount = usedSlots

	return nil
}
