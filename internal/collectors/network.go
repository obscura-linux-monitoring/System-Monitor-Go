package collectors

import (
	"strconv"
	"strings"
	"sync"
	"system-monitor/internal/config"
	"system-monitor/internal/logger"
	"system-monitor/internal/models"
	"time"

	"github.com/shirou/gopsutil/v3/net"
)

// NetworkCollector는 네트워크 메트릭을 수집합니다.
type NetworkCollector struct {
	cachedMetrics     []models.NetworkMetrics
	cachedTime        time.Time
	cacheDuration     time.Duration
	updateFrequency   time.Duration
	mutex             sync.RWMutex
	backgroundRunning bool

	prevCounters map[string]net.IOCountersStat
	prevTime     time.Time
}

// NewNetworkCollector는 새로운 NetworkCollector를 초기화하고 백그라운드 수집을 시작합니다.
func NewNetworkCollector(cfg config.NetworkCollectorSettings) *NetworkCollector {
	collector := &NetworkCollector{
		cacheDuration:   time.Duration(cfg.CacheDurationSeconds) * time.Second,
		updateFrequency: time.Duration(cfg.UpdateFrequencySeconds) * time.Second,
		prevCounters:    make(map[string]net.IOCountersStat),
	}
	collector.startBackgroundCollection()
	return collector
}

func (c *NetworkCollector) startBackgroundCollection() {
	if c.backgroundRunning {
		return
	}
	c.backgroundRunning = true

	c.collectAndCache() // 초기 데이터를 동기적으로 수집하여 캐시를 채웁니다.

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("네트워크 수집기 백그라운드 고루틴 패닉: " + r.(error).Error())
			}
		}()

		ticker := time.NewTicker(c.updateFrequency)
		defer ticker.Stop()

		for range ticker.C {
			c.collectAndCache()
		}
	}()
}

func (c *NetworkCollector) collectAndCache() {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("네트워크 메트릭 수집 중 패닉 발생: " + r.(error).Error())
		}
	}()

	metrics, err := c.collectNetworkMetrics()
	if err != nil {
		logger.Error("백그라운드 네트워크 메트릭 수집 실패: " + err.Error())
		return
	}

	c.mutex.Lock()
	c.cachedMetrics = metrics
	c.cachedTime = time.Now()
	c.mutex.Unlock()
}

// Collect는 캐시된 데이터를 반환합니다. 백그라운드 고루틴이 주기적으로 캐시를 업데이트합니다.
func (c *NetworkCollector) Collect() (interface{}, error) {
	logger.Info("NetworkCollector Collect Start")
	start := time.Now()

	c.mutex.RLock()
	defer c.mutex.RUnlock()

	logger.Info("네트워크 정보 캐시에서 반환")
	logger.Info("NetworkCollector Collect End")
	logger.Info("NetworkCollector Time: " + time.Since(start).String())
	return c.cachedMetrics, nil
}

// collectNetworkMetrics는 실제 네트워크 정보를 수집하는 핵심 로직입니다.
func (c *NetworkCollector) collectNetworkMetrics() ([]models.NetworkMetrics, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	ioCounters, err := net.IOCounters(true)
	if err != nil {
		return nil, err
	}

	countersMap := make(map[string]net.IOCountersStat)
	for _, counter := range ioCounters {
		countersMap[counter.Name] = counter
	}

	now := time.Now()
	deltaTime := now.Sub(c.prevTime).Seconds()

	metrics := make([]models.NetworkMetrics, 0)
	for _, iface := range interfaces {
		status := getInterfaceStatus(iface.Flags)
		isLoopback := false
		for _, flag := range iface.Flags {
			if flag == "loopback" {
				isLoopback = true
				break
			}
		}

		logger.Debug("Processing interface: " + iface.Name + ", Flags: " + strings.Join(iface.Flags, ", ") + ", Status: " + status + ", IsLoopback: " + strconv.FormatBool(isLoopback))

		if isLoopback || status != "up" {
			logger.Debug("Skipping interface: " + iface.Name)
			continue // 루프백 및 비활성 인터페이스는 건너뜁니다.
		}

		metric := models.NetworkMetrics{
			Interface:      iface.Name,
			MAC:            iface.HardwareAddr,
			MTU:            iface.MTU,
			Status:         status,
			ConnectionType: getInterfaceType(iface.Name),
		}

		for _, addr := range iface.Addrs {
			// 주소 문자열(예: "192.168.1.1/24")에서 IP 부분만 추출합니다.
			ip := strings.Split(addr.Addr, "/")[0]
			if strings.Contains(ip, ":") {
				metric.IPv6 = ip
			} else {
				metric.IPv4 = ip
			}
		}

		if counter, ok := countersMap[iface.Name]; ok {
			metric.RxBytes = int64(counter.BytesRecv)
			metric.TxBytes = int64(counter.BytesSent)
			metric.RxPackets = int64(counter.PacketsRecv)
			metric.TxPackets = int64(counter.PacketsSent)
			metric.RxErrors = int64(counter.Errin)
			metric.TxErrors = int64(counter.Errout)
			metric.RxDropped = int64(counter.Dropin)
			metric.TxDropped = int64(counter.Dropout)

			if prevCounter, ok := c.prevCounters[iface.Name]; ok && deltaTime > 0 {
				metric.RxBytesPerSec = float64(counter.BytesRecv-prevCounter.BytesRecv) / deltaTime
				metric.TxBytesPerSec = float64(counter.BytesSent-prevCounter.BytesSent) / deltaTime
			}
		}
		metrics = append(metrics, metric)
	}

	c.mutex.Lock()
	c.prevCounters = countersMap
	c.prevTime = now
	c.mutex.Unlock()

	return metrics, nil
}

func getInterfaceStatus(flags []string) string {
	for _, f := range flags {
		if f == "up" {
			return "up"
		}
	}
	return "down"
}

func getInterfaceType(name string) string {
	lowerName := strings.ToLower(name)
	if strings.HasPrefix(lowerName, "eth") || strings.HasPrefix(lowerName, "en") {
		return "Ethernet"
	}
	if strings.HasPrefix(lowerName, "wl") {
		return "Wi-Fi"
	}
	if strings.HasPrefix(lowerName, "docker") || strings.HasPrefix(lowerName, "br-") || strings.HasPrefix(lowerName, "veth") {
		return "Virtual"
	}
	return "Unknown"
}
