package collectors

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"system-monitor/internal/config"
	"system-monitor/internal/logger"
	"system-monitor/internal/models"
	"time"

	"github.com/godbus/dbus/v5"
)

// ServiceCollector는 시스템 서비스 정보를 수집합니다.
type ServiceCollector struct {
	cachedMetrics     []models.ServiceInfo
	cachedTime        time.Time
	cacheDuration     time.Duration
	updateFrequency   time.Duration
	mutex             sync.RWMutex
	backgroundRunning bool
	conn              *dbus.Conn
}

// NewServiceCollector는 새로운 ServiceCollector를 초기화하고 백그라운드 수집을 시작합니다.
func NewServiceCollector(cfg config.ServiceCollectorSettings) *ServiceCollector {
	collector := &ServiceCollector{
		cacheDuration:   time.Duration(cfg.CacheDurationSeconds) * time.Second,
		updateFrequency: time.Duration(cfg.UpdateFrequencySeconds) * time.Second,
	}
	collector.startBackgroundCollection()
	return collector
}

func (c *ServiceCollector) startBackgroundCollection() {
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

func (c *ServiceCollector) collectAndCache() {
	metrics, err := c.collectServiceMetrics()
	if err == nil {
		c.mutex.Lock()
		c.cachedMetrics = metrics
		c.cachedTime = time.Now()
		c.mutex.Unlock()
	} else {
		logger.Error("백그라운드 서비스 메트릭 수집 실패: " + err.Error())
	}
}

func (c *ServiceCollector) getDbusConnection() (*dbus.Conn, error) {
	if c.conn != nil {
		return c.conn, nil
	}

	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, fmt.Errorf("D-Bus 시스템 버스 연결 실패: %w", err)
	}
	c.conn = conn
	return conn, nil
}

func (c *ServiceCollector) collectServiceMetrics() ([]models.ServiceInfo, error) {
	conn, err := c.getDbusConnection()
	if err != nil {
		return nil, err
	}

	// systemd 서비스 목록 가져오기
	obj := conn.Object("org.freedesktop.systemd1", "/org/freedesktop/systemd1")

	// systemd DBus API에 맞는 구조체 정의
	var units []struct {
		Name        string          // 유닛 이름
		Description string          // 설명
		LoadState   string          // 로드 상태
		ActiveState string          // 활성 상태
		SubState    string          // 서브 상태
		Followed    string          // 팔로우 상태
		Path        dbus.ObjectPath // 객체 경로
		JobId       uint32          // 작업 ID
		JobType     string          // 작업 유형
		JobPath     dbus.ObjectPath // 작업 경로
	}

	err = obj.Call("org.freedesktop.systemd1.Manager.ListUnits", 0).Store(&units)
	if err != nil {
		return nil, fmt.Errorf("systemd 서비스 목록 가져오기 실패: %w", err)
	}

	var services []models.ServiceInfo
	for _, unit := range units {
		// 서비스 유형만 필터링 (.service로 끝나는 이름)
		if !strings.HasSuffix(unit.Name, ".service") {
			continue
		}

		serviceInfo := models.ServiceInfo{
			Name:        strings.TrimSuffix(unit.Name, ".service"),
			LoadState:   unit.LoadState,
			ActiveState: unit.ActiveState,
			SubState:    unit.SubState,
			Status:      getServiceStatus(unit.ActiveState),
			Type:        "systemd",
		}

		// 서비스 활성화 상태 확인
		var unitFileState string
		err = obj.Call("org.freedesktop.systemd1.Manager.GetUnitFileState", 0, unit.Name).Store(&unitFileState)
		if err == nil {
			serviceInfo.Enabled = (unitFileState == "enabled")
		}

		// 서비스 리소스 사용량 정보 수집
		serviceObj := conn.Object("org.freedesktop.systemd1", unit.Path)

		// 메모리 사용량 가져오기
		var memoryUsage uint64
		err = serviceObj.Call("org.freedesktop.DBus.Properties.Get", 0,
			"org.freedesktop.systemd1.Service", "MemoryCurrent").Store(&memoryUsage)
		if err == nil && memoryUsage > 0 {
			serviceInfo.MemoryUsage = strconv.FormatUint(memoryUsage, 10)
		}

		// CPU 사용량은 직접적으로 제공되지 않으므로 프로세스 정보를 통해 추정해야 함
		// 여기서는 기본값 0.0으로 설정
		serviceInfo.CpuUsage = 0.0

		services = append(services, serviceInfo)
	}

	return services, nil
}

// getServiceStatus는 systemd 활성화 상태를 기반으로 서비스 상태 문자열을 반환합니다.
func getServiceStatus(activeState string) string {
	switch activeState {
	case "active":
		return "running"
	case "inactive":
		return "stopped"
	case "failed":
		return "failed"
	case "activating":
		return "starting"
	case "deactivating":
		return "stopping"
	default:
		return "unknown"
	}
}

func (c *ServiceCollector) Collect() (interface{}, error) {
	logger.Info("ServiceCollector Collect Start")
	start := time.Now()

	c.mutex.RLock()
	isCacheValid := time.Since(c.cachedTime) < c.cacheDuration && len(c.cachedMetrics) > 0
	cachedMetrics := c.cachedMetrics
	c.mutex.RUnlock()

	if isCacheValid {
		logger.Info("서비스 정보 캐시에서 반환")
		return cachedMetrics, nil
	}

	logger.Info("서비스 정보를 직접 수집합니다")
	metrics, err := c.collectServiceMetrics()
	if err != nil {
		return nil, err
	}

	c.mutex.Lock()
	c.cachedMetrics = metrics
	c.cachedTime = time.Now()
	c.mutex.Unlock()

	logger.Info("ServiceCollector Collect End")
	logger.Info("Duration: " + time.Since(start).String())
	return metrics, nil
}
