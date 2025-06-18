package collectors

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"system-monitor/internal/logger"
	"system-monitor/internal/models"
	"time"

	"system-monitor/internal/config"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
)

// CPU 수집 관련 상수
const (
	// 백그라운드 수집 관련 상수
	defaultCacheDuration       = 3 * time.Second
	defaultBackgroundSampling  = 500 * time.Millisecond
	defaultBackgroundFrequency = 2 * time.Second

	// 직접 수집 시 샘플링 시간, 테스트할 때는 직접 수집이 많이 사용됨
	defaultDirectSampling = 750 * time.Millisecond // 더 길게 설정
)

type CPUCollector struct {
	cachedPercent     []float64
	cachedPercents    []float64
	cachedTime        time.Time
	cacheDuration     time.Duration
	samplingDuration  time.Duration
	updateFrequency   time.Duration
	directSampling    time.Duration
	mutex             sync.RWMutex
	backgroundRunning bool
}

// NewCPUCollector는 새로운 CPUCollector를 초기화하고 백그라운드 수집을 시작합니다
func NewCPUCollector(cfg config.CPUCollectorSettings) *CPUCollector {
	collector := &CPUCollector{
		cacheDuration:    time.Duration(cfg.CacheDurationSeconds) * time.Second,
		samplingDuration: time.Duration(cfg.BackgroundSamplingMillis) * time.Millisecond,
		updateFrequency:  time.Duration(cfg.BackgroundFrequencySeconds) * time.Second,
		directSampling:   time.Duration(cfg.DirectSamplingMillis) * time.Millisecond,
	}
	collector.startBackgroundCollection()
	return collector
}

// 백그라운드 수집 함수
func (c *CPUCollector) startBackgroundCollection() {
	if c.backgroundRunning {
		return
	}

	c.backgroundRunning = true
	go func() {
		for {
			percent, err := cpu.PercentWithContext(context.Background(), c.samplingDuration, false)
			if err == nil {
				percents, err := cpu.PercentWithContext(context.Background(), c.samplingDuration, true)
				if err == nil {
					c.mutex.Lock()
					c.cachedPercent = percent
					c.cachedPercents = percents
					c.cachedTime = time.Now()
					c.mutex.Unlock()
				}
			}
			time.Sleep(c.updateFrequency)
		}
	}()
}

func parseSize(sizeStr string) int {
	// "32K", "8M" 등의 형식에서 숫자 추출
	re := regexp.MustCompile(`(\d+)`)
	matches := re.FindStringSubmatch(sizeStr)
	if len(matches) > 1 {
		size, _ := strconv.Atoi(matches[1])
		if strings.Contains(sizeStr, "K") {
			return size * 1024
		} else if strings.Contains(sizeStr, "M") {
			return size * 1024 * 1024
		}
		return size
	}
	return 0
}

func getCPUBaseClockSpeed(modelName string) float64 {
	// 예: "Intel(R) Core(TM) i7-7600U CPU @ 2.80GHz" -> 2.80
	re := regexp.MustCompile(`@ ([0-9]+\.[0-9]+)GHz`)
	matches := re.FindStringSubmatch(modelName)
	if len(matches) > 1 {
		baseSpeed, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			return baseSpeed * 1000 // GHz를 MHz로 변환
		}
	}
	// 모델명에서 찾을 수 없으면 현재 최소 클럭 속도를 반환
	return 0
}

func (c *CPUCollector) Collect() (interface{}, error) {
	logger.Info("CPUCollector Collect")
	start := time.Now()

	// 샘플링 시간이 0이면 기본값으로 설정
	if c.directSampling <= 0 {
		c.directSampling = 750 * time.Millisecond
	}

	// CPU 정보 수집
	info, err := cpu.Info()
	if err != nil {
		return nil, err
	}

	logicalCounts, err := cpu.Counts(true)
	if err != nil {
		return nil, err
	}

	physicalCounts, err := cpu.Counts(false)
	if err != nil {
		return nil, err
	}

	isHyperthreading := logicalCounts != physicalCounts

	// 캐시된 CPU 사용률 데이터 확인
	c.mutex.RLock()
	isCacheValid := time.Since(c.cachedTime) < c.cacheDuration &&
		len(c.cachedPercent) > 0 && len(c.cachedPercents) > 0
	percent := c.cachedPercent
	percents := c.cachedPercents
	c.mutex.RUnlock()

	// 캐시가 유효하지 않으면 직접 수집 (최초 실행 시 등)
	if !isCacheValid {
		logger.Info("CPU 사용률을 직접 수집합니다. 샘플링 시간: " + c.directSampling.String())

		// 샘플링 시간 확인
		samplingTime := c.directSampling
		if samplingTime < 500*time.Millisecond {
			samplingTime = 500 * time.Millisecond
		}

		var wg sync.WaitGroup
		var mu sync.Mutex
		var totalErr, coresErr error

		// 두 작업을 병렬로 실행
		wg.Add(2)

		// 전체 CPU 사용률을 고루틴으로 수집
		go func() {
			defer wg.Done()
			totalPercent, err := cpu.PercentWithContext(context.Background(), samplingTime, false)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				totalErr = err
				return
			}
			percent = totalPercent
		}()

		// 개별 코어 사용률을 고루틴으로 수집
		go func() {
			defer wg.Done()
			corePercents, err := cpu.PercentWithContext(context.Background(), samplingTime, true)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				coresErr = err
				return
			}
			percents = corePercents
		}()

		// 모든 고루틴이 완료될 때까지 대기
		wg.Wait()

		// 에러 확인
		if totalErr != nil {
			return nil, totalErr
		}
		if coresErr != nil {
			return nil, coresErr
		}

		// 캐시에도 저장
		c.mutex.Lock()
		c.cachedPercent = percent
		c.cachedPercents = percents
		c.cachedTime = time.Now()
		c.mutex.Unlock()
	}

	// 코어별 정보 설정
	cores := make([]models.CPUCore, len(percents))
	for i, p := range percents {
		cores[i] = models.CPUCore{
			ID:    i,
			Usage: p,
		}
	}

	// CPU 온도 수집
	temps, err := host.SensorsTemperatures()
	var packageTemp float64
	if err != nil {
		logger.Error("CPU 온도 센서 정보를 가져오는데 실패했습니다: " + err.Error())
	} else {
		coreTempRegex := regexp.MustCompile(`core_(\d+)`)
		for _, temp := range temps {
			key := strings.ToLower(temp.SensorKey)

			// 코어별 온도 확인
			matches := coreTempRegex.FindStringSubmatch(key)
			if len(matches) > 1 {
				coreID, err := strconv.Atoi(matches[1])
				if err == nil && coreID < len(cores) {
					if isHyperthreading {
						cores[coreID*2].Temperature = temp.Temperature
						cores[coreID*2+1].Temperature = temp.Temperature
					} else {
						cores[coreID].Temperature = temp.Temperature
					}
					continue
				}
			}

			// 일반적인 코어 온도 센서 키 확인 (e.g., temp1_input, temp2_input for cores)
			if strings.HasPrefix(key, "temp") && strings.HasSuffix(key, "_input") {
				re := regexp.MustCompile(`temp(\d+)_input`)
				submatches := re.FindStringSubmatch(key)
				if len(submatches) > 1 {
					coreID, err := strconv.Atoi(submatches[1])
					if err == nil {
						// Assuming temp1_input is package, and temp2... are cores
						// This logic may need adjustment depending on the system's sensor naming
						coreIndex := coreID - 2
						if coreIndex >= 0 && coreIndex < len(cores) {
							cores[coreIndex].Temperature = temp.Temperature
							continue
						}
					}
				}
			}

			// 패키지 온도 확인
			if strings.Contains(key, "package") || strings.HasPrefix(key, "cpu_temp") || key == "temp1_input" {
				packageTemp = temp.Temperature
			}
		}

		if packageTemp == 0 {
			// Find a fallback temperature if no specific package temperature was found
			for _, temp := range temps {
				if temp.Temperature > 0 {
					key := strings.ToLower(temp.SensorKey)
					if strings.Contains(key, "input") { // A generic check
						packageTemp = temp.Temperature
						break
					}
				}
			}
		}
	}

	flags := info[0].Flags

	detailedInfo, err := getCPUDetailedInfo()
	if err != nil {
		logger.Error("CPU 상세 정보를 가져오는데 실패했습니다: " + err.Error())
	}

	// CPUMetrics 구조체 생성
	metrics := models.CPUMetrics{
		Architecture:      info[0].ModelName,
		Model:             info[0].ModelName,
		Vendor:            info[0].VendorID,
		CacheSize:         int(info[0].CacheSize),
		ClockSpeed:        info[0].Mhz,
		TotalCores:        physicalCounts,
		TotalLogicalCores: logicalCounts,
		Usage:             percent[0],
		Temperature:       packageTemp,
		Cores:             cores,
		HasVMX:            contains(flags, "vmx"),
		HasSVM:            contains(flags, "svm"),
		HasAVX:            contains(flags, "avx"),
		HasAVX2:           contains(flags, "avx2"),
		HasNEON:           contains(flags, "neon"),
		HasSVE:            contains(flags, "sve"),
		IsHyperthreading:  isHyperthreading,
		L1CacheSize:       detailedInfo["l1_cache"].(int),
		L2CacheSize:       detailedInfo["l2_cache"].(int),
		L3CacheSize:       detailedInfo["l3_cache"].(int),
		BaseClockSpeed:    detailedInfo["base_clock_speed"].(float64),
		MaxClockSpeed:     detailedInfo["max_clock_speed"].(float64),
		MinClockSpeed:     detailedInfo["min_clock_speed"].(float64),
	}

	logger.Info("CPUCollector Collect End")
	logger.Info("Duration: " + time.Since(start).String())
	return metrics, nil
}

func contains(slice []string, str string) bool {
	for _, v := range slice {
		if v == str {
			return true
		}
	}
	return false
}

func getCPUDetailedInfo() (map[string]interface{}, error) {
	cmd := exec.Command("lscpu")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	info := make(map[string]interface{})
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// lscpu 결과에서 모델명 추출
		if strings.Contains(key, "Model name") {
			info["model_name"] = value
		} else if strings.Contains(key, "L1d cache") {
			info["l1_cache"] = parseSize(value)
		} else if strings.Contains(key, "L2 cache") {
			info["l2_cache"] = parseSize(value)
		} else if strings.Contains(key, "L3 cache") {
			info["l3_cache"] = parseSize(value)
		} else if strings.Contains(key, "CPU min MHz") {
			info["min_clock_speed"], _ = strconv.ParseFloat(value, 64)
		} else if strings.Contains(key, "CPU max MHz") {
			info["max_clock_speed"], _ = strconv.ParseFloat(value, 64)
		}
	}

	// nil 체크 추가
	if model, ok := info["model_name"]; ok && model != nil {
		info["base_clock_speed"] = getCPUBaseClockSpeed(model.(string))
	} else {
		info["base_clock_speed"] = 0
		// model_name이 없을 경우 기본값 설정
		info["model_name"] = ""
	}

	return info, nil
}
