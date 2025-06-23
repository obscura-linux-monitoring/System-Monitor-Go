package collectors

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"system-monitor/internal/config"
	"system-monitor/internal/logger"
	"system-monitor/internal/models"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// DockerStats는 Docker API가 반환하는 실제 JSON 데이터 구조를 나타냅니다.
// 공식 SDK에서 StatsJSON 타입이 변경됨에 따라 로그를 분석하여 직접 정의했습니다.
type DockerStats struct {
	Read      time.Time `json:"read"`
	ID        string    `json:"id"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PercpuUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
	BlkioStats struct {
		IoServiceBytesRecursive []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

// ContainerCollector는 도커 컨테이너 정보를 수집합니다.
type ContainerCollector struct {
	dockerClient      *client.Client
	cachedMetrics     []models.DockerContainer
	cachedTime        time.Time
	cacheDuration     time.Duration
	updateFrequency   time.Duration
	mutex             sync.RWMutex
	backgroundRunning bool
}

// NewContainerCollector는 새로운 ContainerCollector를 초기화하고 백그라운드 수집을 시작합니다.
func NewContainerCollector(cfg config.DockerCollectorSettings) *ContainerCollector {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Warn("Docker 클라이언트 생성 실패, Docker가 실행 중인지 확인하세요: " + err.Error())
		// Docker 클라이언트 생성 실패 시에도 수집기 객체는 반환
		return &ContainerCollector{
			dockerClient:    nil,
			cacheDuration:   time.Duration(cfg.CacheDurationSeconds) * time.Second,
			updateFrequency: time.Duration(cfg.UpdateFrequencySeconds) * time.Second,
		}
	}

	collector := &ContainerCollector{
		dockerClient:    cli,
		cacheDuration:   time.Duration(cfg.CacheDurationSeconds) * time.Second,
		updateFrequency: time.Duration(cfg.UpdateFrequencySeconds) * time.Second,
	}
	collector.startBackgroundCollection()
	return collector
}

func (c *ContainerCollector) startBackgroundCollection() {
	if c.backgroundRunning || c.dockerClient == nil {
		if c.dockerClient == nil {
			logger.Warn("Docker 클라이언트가 없어 컨테이너 정보 백그라운드 수집을 시작하지 않습니다.")
		}
		return
	}
	c.backgroundRunning = true

	go func() {
		c.collectAndCache()

		ticker := time.NewTicker(c.updateFrequency)
		defer ticker.Stop()

		for range ticker.C {
			c.collectAndCache()
		}
	}()
}

func (c *ContainerCollector) collectAndCache() {
	metrics, err := c.collectContainerMetrics()
	if err != nil {
		logger.Error("백그라운드 컨테이너 메트릭 수집 실패: " + err.Error())
		return
	}

	c.mutex.Lock()
	c.cachedMetrics = metrics
	c.cachedTime = time.Now()
	c.mutex.Unlock()
}

// Collect는 캐시가 유효하면 캐시된 데이터를, 아니면 새로 수집한 데이터를 반환합니다.
func (c *ContainerCollector) Collect() (interface{}, error) {
	logger.Info("ContainerCollector Collect Start")
	start := time.Now()

	if c.dockerClient == nil {
		return []models.DockerContainer{}, nil
	}

	c.mutex.RLock()
	isCacheValid := time.Since(c.cachedTime) < c.cacheDuration && c.cachedMetrics != nil
	cachedMetrics := c.cachedMetrics
	c.mutex.RUnlock()

	if isCacheValid {
		logger.Info("컨테이너 정보 캐시에서 반환")
		logger.Info("ContainerCollector Collect End")
		logger.Info("ContainerCollector Time: " + time.Since(start).String())
		return cachedMetrics, nil
	}

	logger.Info("컨테이너 정보를 직접 수집합니다")
	metrics, err := c.collectContainerMetrics()
	if err != nil {
		return nil, err
	}

	logger.Info("ContainerCollector Collect End")
	logger.Info("ContainerCollector Time: " + time.Since(start).String())
	return metrics, nil
}

// collectContainerMetrics는 실제 컨테이너 정보를 수집하는 핵심 로직입니다.
func (c *ContainerCollector) collectContainerMetrics() ([]models.DockerContainer, error) {
	ctx := context.Background()
	containers, err := c.dockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	metricsChan := make(chan models.DockerContainer, len(containers))

	for _, cont := range containers {
		wg.Add(1)
		go func(cont types.Container) {
			defer wg.Done()
			details, err := c.dockerClient.ContainerInspect(ctx, cont.ID)
			if err != nil {
				logger.Warn("컨테이너 상세 정보 가져오기 실패 " + cont.ID[:12] + ": " + err.Error())
				return
			}

			stats, err := c.dockerClient.ContainerStats(ctx, cont.ID, false)
			if err != nil {
				logger.Warn("컨테이너 스탯 가져오기 실패 " + cont.ID[:12] + ": " + err.Error())
				return
			}
			defer stats.Body.Close()

			bodyBytes, err := io.ReadAll(stats.Body)
			if err != nil {
				logger.Warn("컨테이너 스탯 본문 읽기 실패 " + cont.ID[:12] + ": " + err.Error())
				return
			}

			var currentStats DockerStats
			if err := json.Unmarshal(bodyBytes, &currentStats); err != nil {
				logger.Warn("컨테이너 스탯 JSON 파싱 실패 " + cont.ID[:12] + ": " + err.Error())
				return
			}

			// --- 메트릭 계산 ---
			cpuUsage := c.calculateCPUUsage(&currentStats)
			memUsage, memLimit, memPercent := c.calculateMemoryUsage(&currentStats)
			netRx, netTx := c.calculateNetworkIO(&currentStats)
			blkRead, blkWrite := c.calculateBlockIO(&currentStats)

			// --- 모델 변환 ---
			metric := models.DockerContainer{
				ID:             cont.ID,
				Name:           strings.TrimPrefix(cont.Names[0], "/"),
				Image:          cont.Image,
				Status:         cont.Status,
				Created:        details.Created,
				CPUUsage:       cpuUsage,
				MemoryUsage:    memUsage,
				MemoryLimit:    memLimit,
				MemoryPercent:  memPercent,
				NetworkRxBytes: netRx,
				NetworkTxBytes: netTx,
				BlockRead:      blkRead,
				BlockWrite:     blkWrite,
				PIDs:           int(currentStats.PidsStats.Current),
				Restarts:       details.RestartCount,
				Labels:         convertLabels(details.Config.Labels),
				Health:         convertHealth(details.State.Health),
				Ports:          convertPorts(details.NetworkSettings.Ports),
				Networks:       convertNetworks(details.NetworkSettings.Networks, &currentStats),
				Volumes:        convertVolumes(details.Mounts),
				Env:            convertEnv(details.Config.Env),
			}
			metricsChan <- metric
		}(cont)
	}

	go func() {
		wg.Wait()
		close(metricsChan)
	}()

	var metrics []models.DockerContainer
	for metric := range metricsChan {
		metrics = append(metrics, metric)
	}

	return metrics, nil
}

func (c *ContainerCollector) calculateCPUUsage(current *DockerStats) float64 {
	var (
		cpuPercent  = 0.0
		cpuDelta    = float64(current.CPUStats.CPUUsage.TotalUsage) - float64(current.PreCPUStats.CPUUsage.TotalUsage)
		systemDelta = float64(current.CPUStats.SystemCPUUsage) - float64(current.PreCPUStats.SystemCPUUsage)
		onlineCPUs  = float64(current.CPUStats.OnlineCPUs)
	)

	if onlineCPUs == 0.0 {
		onlineCPUs = float64(len(current.CPUStats.CPUUsage.PercpuUsage))
	}

	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent = (cpuDelta / systemDelta) * onlineCPUs * 100.0
	}
	return cpuPercent
}

func (c *ContainerCollector) calculateMemoryUsage(current *DockerStats) (usage, limit int64, percent float64) {
	usage = int64(current.MemoryStats.Usage)
	limit = int64(current.MemoryStats.Limit)
	if limit > 0 {
		percent = float64(usage) / float64(limit) * 100.0
	}
	return
}

func (c *ContainerCollector) calculateNetworkIO(current *DockerStats) (rx, tx int64) {
	for _, network := range current.Networks {
		rx += int64(network.RxBytes)
		tx += int64(network.TxBytes)
	}
	return
}

func (c *ContainerCollector) calculateBlockIO(current *DockerStats) (read, write int64) {
	for _, bioStat := range current.BlkioStats.IoServiceBytesRecursive {
		switch bioStat.Op {
		case "Read":
			read += int64(bioStat.Value)
		case "Write":
			write += int64(bioStat.Value)
		}
	}
	return
}

// --- Docker API 타입에서 내부 모델로 변환하는 헬퍼 함수들 ---

func convertLabels(m map[string]string) []models.ContainerLabel {
	if m == nil {
		return nil
	}
	labels := make([]models.ContainerLabel, 0, len(m))
	for k, v := range m {
		labels = append(labels, models.ContainerLabel{Key: k, Value: v})
	}
	return labels
}

func convertHealth(h *types.Health) models.ContainerHealth {
	if h == nil {
		return models.ContainerHealth{Status: "unknown"}
	}

	health := models.ContainerHealth{
		Status:        h.Status,
		FailingStreak: h.FailingStreak,
	}

	if len(h.Log) > 0 {
		health.LastCheckOutput = h.Log[len(h.Log)-1].Output
	}

	if health.Status == "" {
		health.Status = "unknown"
	}

	return health
}

func convertPorts(p nat.PortMap) []models.ContainerPort {
	if p == nil {
		return nil
	}
	ports := make([]models.ContainerPort, 0)
	for containerPort, hostBindings := range p {
		if hostBindings == nil {
			continue
		}
		for _, binding := range hostBindings {
			ports = append(ports, models.ContainerPort{
				ContainerPort: containerPort.Port(),
				HostPort:      binding.HostPort,
				Protocol:      containerPort.Proto(),
			})
		}
	}
	return ports
}

func convertNetworks(n map[string]*network.EndpointSettings, stats *DockerStats) []models.ContainerNetwork {
	if n == nil {
		return nil
	}
	networks := make([]models.ContainerNetwork, 0, len(n))

	// 휴리스틱: ContainerStats API가 인터페이스 이름을 'eth0' 등으로 반환하고,
	// Inspect API는 'my-app-network' 같은 논리적 Docker 네트워크 이름을 반환하여 직접적인 매칭이 어렵습니다.
	// 가장 일반적인 '컨테이너당 단일 네트워크 인터페이스' 케이스를 처리하기 위해,
	// stats에 인터페이스가 하나만 존재하면 해당 I/O 통계를 모든 논리 네트워크에 적용합니다.
	if stats != nil && stats.Networks != nil && len(stats.Networks) == 1 {
		var singleNetStats *struct {
			RxBytes uint64 `json:"rx_bytes"`
			TxBytes uint64 `json:"tx_bytes"`
		}
		// 단일 인터페이스(예: 'eth0')의 통계를 가져옵니다.
		for _, s := range stats.Networks {
			statCopy := s // 반복자 변수를 직접 참조하지 않기 위해 복사
			singleNetStats = &statCopy
			break
		}

		if singleNetStats != nil {
			for name, settings := range n {
				networks = append(networks, models.ContainerNetwork{
					Name:    name,
					IP:      settings.IPAddress,
					MAC:     settings.MacAddress,
					RxBytes: singleNetStats.RxBytes,
					TxBytes: singleNetStats.TxBytes,
				})
			}
			return networks
		}
	}

	// 휴리스틱 적용이 불가능한 경우 (인터페이스가 없거나 여러 개인 경우), I/O 통계 없이 기본 정보만 채웁니다.
	for name, settings := range n {
		networks = append(networks, models.ContainerNetwork{
			Name: name,
			IP:   settings.IPAddress,
			MAC:  settings.MacAddress,
		})
	}
	return networks
}

func convertVolumes(m []types.MountPoint) []models.ContainerVolume {
	if m == nil {
		return nil
	}
	volumes := make([]models.ContainerVolume, 0, len(m))
	for _, mount := range m {
		volumes = append(volumes, models.ContainerVolume{
			Source:      mount.Source,
			Destination: mount.Destination,
			Mode:        mount.Mode,
			Type:        string(mount.Type),
		})
	}
	return volumes
}

func convertEnv(e []string) []models.ContainerEnv {
	if e == nil {
		return nil
	}
	envs := make([]models.ContainerEnv, 0, len(e))
	for _, envVar := range e {
		parts := strings.SplitN(envVar, "=", 2)
		key := parts[0]
		value := ""
		if len(parts) > 1 {
			value = parts[1]
		}
		envs = append(envs, models.ContainerEnv{Key: key, Value: value})
	}
	return envs
}
