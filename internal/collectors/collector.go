package collectors

import (
	"strconv"
	"strings"
	"sync"
	"system-monitor/internal/common"
	"system-monitor/internal/config"
	"system-monitor/internal/logger"
	"system-monitor/internal/models"
	"time"
)

// Collector는 특정 유형의 시스템 메트릭을 수집하기 위한 인터페이스입니다.
type Collector interface {
	Collect() (interface{}, error)
}

// MasterCollector는 다양한 수집기를 조율하여 모든 시스템 메트릭을 수집합니다.
type MasterCollector struct {
	collectors   map[string]Collector
	metricsQueue common.Queue
	userKey      string
	localKey     string
}

// NewMasterCollector는 새 MasterCollector를 초기화하고 반환합니다.
// 각 카테고리별 수집기를 등록합니다.
func NewMasterCollector(cfg config.Config) *MasterCollector {
	mc := &MasterCollector{
		collectors:   make(map[string]Collector),
		metricsQueue: common.NewQueue(100),
		userKey:      cfg.UserKey,
		localKey:     cfg.LocalKey,
	}
	mc.AddCollector("system", NewSystemCollector(cfg.Collectors.System))
	mc.AddCollector("cpu", NewCPUCollector(cfg.Collectors.CPU))
	mc.AddCollector("memory", NewMemoryCollector(cfg.Collectors.Memory))
	mc.AddCollector("disk", NewDiskCollector(cfg.Collectors.Disk))
	mc.AddCollector("container", NewContainerCollector(cfg.Collectors.Docker))
	mc.AddCollector("network", NewNetworkCollector(cfg.Collectors.Network))
	mc.AddCollector("process", NewProcessCollector(cfg.Collectors.Process))
	mc.AddCollector("service", NewServiceCollector(cfg.Collectors.Service))
	return mc
}

// AddCollector는 MasterCollector에 수집기를 추가합니다.
func (mc *MasterCollector) AddCollector(name string, collector Collector) {
	mc.collectors[name] = collector
}

// CollectAll은 등록된 모든 수집기에서 메트릭을 수집합니다.
func (mc *MasterCollector) CollectAll(userID string, key string) (*models.SystemMetrics, error) {
	logger.Info("메트릭 수집 시작: userID=" + userID + ", key=" + key)
	start := time.Now()
	metrics := &models.SystemMetrics{
		USER_ID:   userID,
		Key:       key,
		Timestamp: time.Now(),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var collectionErrors []string

	for name, collector := range mc.collectors {
		wg.Add(1)
		go func(name string, c Collector) {
			defer wg.Done()
			logger.Info("수집기 시작: " + name)
			data, err := c.Collect()
			if err != nil {
				logger.Error("수집기 오류: " + name + ", error: " + err.Error())
				mu.Lock()
				collectionErrors = append(collectionErrors, name+": "+err.Error())
				mu.Unlock()
				return
			}

			logger.Info("수집기 성공: " + name)
			mu.Lock()
			defer mu.Unlock()

			switch v := data.(type) {
			case models.SystemInfo:
				metrics.System = v
			case models.CPUMetrics:
				metrics.CPU = v
			case models.MemoryMetrics:
				metrics.Memory = v
			case []models.DiskMetrics:
				metrics.Disk = v
			case []models.NetworkMetrics:
				metrics.Network = v
			case []models.ProcessInfo:
				metrics.Processes = v
			case []models.DockerContainer:
				metrics.Containers = v
			case []models.ServiceInfo:
				metrics.Services = v
			}
		}(name, collector)
	}

	wg.Wait()

	// 오류가 있는 경우에도 수집된 데이터는 반환
	if len(collectionErrors) > 0 {
		logger.Warn("일부 수집기에서 오류 발생: " + strings.Join(collectionErrors, "; "))
	}

	logger.Info("모든 메트릭 수집 완료, 소요 시간: " + time.Since(start).String())
	return metrics, nil
}

// Start 지정된 간격으로 메트릭 수집을 시작합니다.
func (mc *MasterCollector) Start(intervalSeconds int) {
	logger.Info("메트릭 수집기 시작: 간격=" + time.Duration(intervalSeconds).String() + "초")

	// 수집 고루틴 시작
	go func() {
		ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			metrics, err := mc.CollectAll(mc.userKey, mc.localKey)
			if err != nil {
				logger.Error("메트릭 수집 실패: " + err.Error())
				continue
			}

			// 수집된 메트릭을 큐에 추가
			mc.metricsQueue.Push(*metrics)
			logger.Info("메트릭이 큐에 추가되었습니다. 큐 크기: " + strconv.Itoa(mc.metricsQueue.Size()))
		}
	}()
}

// Stop 메트릭 수집을 중지합니다.
func (mc *MasterCollector) Stop() {
	logger.Info("메트릭 수집기 중지")
	// 수집 중지 코드...
}

// GetDataQueue 수집된 메트릭을 저장하는 큐를 반환합니다.
func (mc *MasterCollector) GetDataQueue() common.Queue {
	// 큐 생성 또는 반환 로직
	return mc.metricsQueue
}
