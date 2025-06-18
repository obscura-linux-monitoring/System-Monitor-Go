package collectors

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"system-monitor/internal/config"
	"system-monitor/internal/logger"
	"system-monitor/internal/models"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

var ignoredFSTypes = map[string]struct{}{
	"autofs": {}, "binfmt_misc": {}, "bpf": {}, "cgroup": {}, "cgroup2": {},
	"configfs": {}, "debugfs": {}, "devpts": {}, "devtmpfs": {}, "fusectl": {},
	"hugetlbfs": {}, "mqueue": {}, "nsfs": {}, "overlay": {}, "proc": {},
	"pstore": {}, "securityfs": {}, "squashfs": {}, "sysfs": {}, "tracefs": {},
	"tmpfs": {},
}

var blockDeviceCache = make(map[string]string)
var blockDeviceCacheMutex = &sync.Mutex{}
var partitionRegexp = regexp.MustCompile(`p?\d+$`)

// getBlockDeviceFromPartition은 파티션명으로부터 블록 디바이스명을 찾습니다. (예: sda1 -> sda)
func getBlockDeviceFromPartition(partition string) string {
	blockDeviceCacheMutex.Lock()
	defer blockDeviceCacheMutex.Unlock()
	if dev, ok := blockDeviceCache[partition]; ok {
		return dev
	}

	// dm-X 와 같이 숫자로 끝나는 장치 이름을 파티션으로 오인하지 않도록 합니다.
	// `dm-`으로 시작하는 장치는 그 자체가 블록 디바이스입니다.
	if strings.HasPrefix(partition, "dm-") {
		blockDeviceCache[partition] = partition
		return partition
	}

	// nvme0n1p1 -> nvme0n1, sda1 -> sda, mmcblk0p1 -> mmcblk0
	baseName := partitionRegexp.ReplaceAllString(partition, "")

	// 정규식으로 변경이 일어난 경우에만 /sys/block 에서 검증합니다.
	if baseName != partition {
		if _, err := os.Stat(filepath.Join("/sys/block", baseName)); err == nil {
			blockDeviceCache[partition] = baseName
			return baseName
		}
	}

	// 검증 실패 시 또는 변경이 없었을 경우 원래 파티션명을 블록 디바이스로 간주합니다.
	blockDeviceCache[partition] = partition
	return partition
}

func getDiskInfo(blockDeviceName string) (modelName string, diskType string) {
	modelName = "N/A"
	diskType = "N/A"

	// 파티션 이름(e.g. sda3)이 들어올 경우 실제 블록 디바이스(e.g. sda) 이름으로 변환합니다.
	baseDeviceName := getBlockDeviceFromPartition(blockDeviceName)
	sysBlockPath := filepath.Join("/sys/block", baseDeviceName)

	// dm- (device-mapper) 장치는 물리 디스크에 대한 논리적 래퍼일 수 있습니다.
	// 실제 물리 디스크 정보를 얻기 위해 하위(slave) 디바이스를 확인합니다.
	if strings.HasPrefix(baseDeviceName, "dm-") {
		slavesDir := filepath.Join(sysBlockPath, "slaves")
		if files, err := ioutil.ReadDir(slavesDir); err == nil && len(files) > 0 {
			// 논리 볼륨은 여러 물리 디스크에 걸쳐있을 수 있습니다.
			// 여기서는 첫 번째 슬레이브 디바이스를 기준으로 정보를 가져옵니다.
			return getDiskInfo(files[0].Name())
		}
	}

	// /sys/block/{blockDeviceName}/device/model 에서 모델명을 읽어옵니다.
	modelPath := filepath.Join(sysBlockPath, "device/model")
	if modelBytes, err := ioutil.ReadFile(modelPath); err == nil {
		modelName = strings.TrimSpace(string(modelBytes))
	}

	// /sys/block/{blockDeviceName}/queue/rotational 값을 통해 SSD/HDD를 구분합니다. (0: SSD, 1: HDD)
	rotationalPath := filepath.Join(sysBlockPath, "queue/rotational")
	if rotationalBytes, err := ioutil.ReadFile(rotationalPath); err == nil {
		rotational := strings.TrimSpace(string(rotationalBytes))
		if rotational == "0" {
			diskType = "SSD"
		} else if rotational == "1" {
			diskType = "HDD"
		}
	}

	return modelName, diskType
}

// DiskCollector는 디스크 메트릭을 수집합니다.
type DiskCollector struct {
	cachedMetrics     []models.DiskMetrics
	cachedTime        time.Time
	cacheDuration     time.Duration
	updateFrequency   time.Duration
	mutex             sync.RWMutex
	backgroundRunning bool
	prevIOCounters    map[string]disk.IOCountersStat
	prevTime          time.Time
}

// NewDiskCollector는 새로운 DiskCollector를 초기화하고 백그라운드 수집을 시작합니다.
func NewDiskCollector(cfg config.DiskCollectorSettings) *DiskCollector {
	collector := &DiskCollector{
		cacheDuration:   time.Duration(cfg.CacheDurationSeconds) * time.Second,
		updateFrequency: time.Duration(cfg.UpdateFrequencySeconds) * time.Second,
		prevIOCounters:  make(map[string]disk.IOCountersStat),
	}
	collector.startBackgroundCollection()
	return collector
}

func (c *DiskCollector) startBackgroundCollection() {
	if c.backgroundRunning {
		return
	}
	c.backgroundRunning = true

	go func() {
		// 첫 수집을 즉시 실행하여 초기 데이터를 확보합니다.
		// 이렇게 하면 에이전트 시작 후 첫 Collect 요청 시 캐시된 데이터를 즉시 제공할 수 있습니다.
		c.collectAndCache()

		ticker := time.NewTicker(c.updateFrequency)
		defer ticker.Stop()

		for range ticker.C {
			c.collectAndCache()
		}
	}()
}

func (c *DiskCollector) collectAndCache() {
	metrics, err := c.collectDiskMetrics()
	if err == nil {
		c.mutex.Lock()
		c.cachedMetrics = metrics
		c.cachedTime = time.Now()
		c.mutex.Unlock()
	} else {
		logger.Error("백그라운드 디스크 메트릭 수집 실패: " + err.Error())
	}
}

func (c *DiskCollector) collectDiskMetrics() ([]models.DiskMetrics, error) {
	partitions, err := disk.Partitions(false)
	if err != nil {
		return nil, err
	}

	ioCounters, err := disk.IOCounters()
	if err != nil {
		logger.Warn("디스크 IO 카운터 정보 가져오기 실패: " + err.Error())
		ioCounters = make(map[string]disk.IOCountersStat)
	}

	c.mutex.Lock()
	prevCounters := c.prevIOCounters
	prevTime := c.prevTime
	c.prevIOCounters = ioCounters
	c.prevTime = time.Now()
	c.mutex.Unlock()

	var duration float64
	isFirstCollection := prevTime.IsZero()
	if !isFirstCollection {
		duration = time.Since(prevTime).Seconds()
	}

	if duration <= 0 {
		duration = 1.0
	}

	var metrics []models.DiskMetrics
	for _, p := range partitions {
		if _, ok := ignoredFSTypes[p.Fstype]; ok {
			continue
		}

		usage, err := disk.Usage(p.Mountpoint)
		dm := models.DiskMetrics{
			Device:         p.Device,
			MountPoint:     p.Mountpoint,
			FilesystemType: p.Fstype,
			IsSystemDisk:   p.Mountpoint == "/",
			IsPageFileDisk: p.Fstype == "swap",
		}

		if err != nil {
			logger.Warn("디스크 사용량 정보 가져오기 실패 " + p.Mountpoint + ": " + err.Error())
		} else {
			dm.Total = int64(usage.Total)
			dm.Used = int64(usage.Used)
			dm.Free = int64(usage.Free)
			dm.UsagePercent = usage.UsedPercent
			dm.InodesTotal = int64(usage.InodesTotal)
			dm.InodesUsed = int64(usage.InodesUsed)
			dm.InodesFree = int64(usage.InodesFree)
		}

		ioDeviceName := filepath.Base(p.Device)
		blockDeviceName := getBlockDeviceFromPartition(ioDeviceName)
		dm.ModelName, dm.Type = getDiskInfo(blockDeviceName)

		if strings.HasPrefix(ioDeviceName, "nvme") {
			// gopsutil의 IOCounters가 nvme의 경우 파티션(nvme0n1p1)이 아닌
			// 디바이스(nvme0n1) 레벨의 통계를 제공하는 경우가 있어,
			// blockDeviceName을 사용해 조회합니다.
			if _, ok := ioCounters[blockDeviceName]; ok {
				ioDeviceName = blockDeviceName
			}
		}

		// 먼저 파티션/장치 이름으로 IO 통계를 찾아봅니다.
		currentIO, ok := ioCounters[ioDeviceName]
		if !ok {
			currentIO, ok = ioCounters[blockDeviceName]
			if ok {
				// 기반 장치 이름으로 통계를 찾았으면, 이 이름을 키로 사용합니다.
				ioDeviceName = blockDeviceName
			}
		}

		if ok {
			ioStats := models.DiskIOStats{
				ReadBytes:    int64(currentIO.ReadBytes),
				WriteBytes:   int64(currentIO.WriteBytes),
				Reads:        int64(currentIO.ReadCount),
				Writes:       int64(currentIO.WriteCount),
				ReadTime:     int64(currentIO.ReadTime),
				WriteTime:    int64(currentIO.WriteTime),
				IOInProgress: int64(currentIO.IopsInProgress),
				IOTime:       int64(currentIO.IoTime),
			}
			if prevIO, ok := prevCounters[ioDeviceName]; ok && !isFirstCollection {
				ioStats.ReadBytesPerSec = float64(currentIO.ReadBytes-prevIO.ReadBytes) / duration
				ioStats.WriteBytesPerSec = float64(currentIO.WriteBytes-prevIO.WriteBytes) / duration
				ioStats.ReadsPerSec = float64(currentIO.ReadCount-prevIO.ReadCount) / duration
				ioStats.WritesPerSec = float64(currentIO.WriteCount-prevIO.WriteCount) / duration
			}
			dm.IOStats = ioStats
		}
		metrics = append(metrics, dm)
	}

	return metrics, nil
}

func (c *DiskCollector) Collect() (interface{}, error) {
	logger.Info("DiskCollector Collect Start")
	start := time.Now()

	c.mutex.RLock()
	isCacheValid := time.Since(c.cachedTime) < c.cacheDuration && len(c.cachedMetrics) > 0
	cachedMetrics := c.cachedMetrics
	c.mutex.RUnlock()

	if isCacheValid {
		logger.Info("디스크 정보 캐시에서 반환")
		return cachedMetrics, nil
	}

	logger.Info("디스크 정보를 직접 수집합니다")
	metrics, err := c.collectDiskMetrics()
	if err != nil {
		return nil, err
	}

	c.mutex.Lock()
	c.cachedMetrics = metrics
	c.cachedTime = time.Now()
	c.mutex.Unlock()

	logger.Info("DiskCollector Collect End")
	logger.Info("Duration: " + time.Since(start).String())
	return metrics, nil
}
