package config

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"system-monitor/internal/logger"
)

// Config 구조체는 시스템 모니터링 설정을 저장합니다
type Config struct {
	CollectionInterval int               `json:"collectionInterval"`
	SendingInterval    int               `json:"sendingInterval"`
	ServerUrl          string            `json:"serverUrl"`
	LocalKey           string            `json:"localKey"`
	UserKey            string            `json:"userKey"`
	Collectors         CollectorSettings `json:"collectors"`
}

// CollectorSettings는 각 수집기별 설정을 포함합니다.
type CollectorSettings struct {
	CPU     CPUCollectorSettings     `json:"cpu"`
	System  SystemCollectorSettings  `json:"system"`
	Memory  MemoryCollectorSettings  `json:"memory"`
	Disk    DiskCollectorSettings    `json:"disk"`
	Network NetworkCollectorSettings `json:"network"`
	Docker  DockerCollectorSettings  `json:"docker"`
	Process ProcessCollectorSettings `json:"process"`
	Service ServiceCollectorSettings `json:"service"`
}

// CPUCollectorSettings는 CPU 수집기 설정을 정의합니다.
type CPUCollectorSettings struct {
	CacheDurationSeconds       int `json:"cacheDurationSeconds"`
	BackgroundSamplingMillis   int `json:"backgroundSamplingMillis"`
	BackgroundFrequencySeconds int `json:"backgroundFrequencySeconds"`
	DirectSamplingMillis       int `json:"directSamplingMillis"`
}

// SystemCollectorSettings는 시스템 수집기 설정을 정의합니다.
type SystemCollectorSettings struct {
	CacheDurationSeconds   int `json:"cacheDurationSeconds"`
	UpdateFrequencySeconds int `json:"updateFrequencySeconds"`
}

// MemoryCollectorSettings는 메모리 수집기 설정을 정의합니다.
type MemoryCollectorSettings struct {
	CacheDurationSeconds   int `json:"cacheDurationSeconds"`
	UpdateFrequencySeconds int `json:"updateFrequencySeconds"`
}

// DiskCollectorSettings는 디스크 수집기 설정을 정의합니다.
type DiskCollectorSettings struct {
	CacheDurationSeconds   int `json:"cacheDurationSeconds"`
	UpdateFrequencySeconds int `json:"updateFrequencySeconds"`
}

// NetworkCollectorSettings는 네트워크 수집기 설정을 정의합니다.
type NetworkCollectorSettings struct {
	CacheDurationSeconds   int `json:"cacheDurationSeconds"`
	UpdateFrequencySeconds int `json:"updateFrequencySeconds"`
}

// DockerCollectorSettings는 도커 수집기 설정을 정의합니다.
type DockerCollectorSettings struct {
	CacheDurationSeconds   int `json:"cacheDurationSeconds"`
	UpdateFrequencySeconds int `json:"updateFrequencySeconds"`
}

// ProcessCollectorSettings는 프로세스 수집기 설정을 정의합니다.
type ProcessCollectorSettings struct {
	CacheDurationSeconds   int `json:"cacheDurationSeconds"`
	UpdateFrequencySeconds int `json:"updateFrequencySeconds"`
}

// ServiceCollectorSettings는 서비스 수집기 설정을 정의합니다.
type ServiceCollectorSettings struct {
	CacheDurationSeconds   int `json:"cacheDurationSeconds"`
	UpdateFrequencySeconds int `json:"updateFrequencySeconds"`
}

// DefaultConfig는 기본 설정값을 반환합니다
func DefaultConfig() Config {
	return Config{
		CollectionInterval: 5,
		SendingInterval:    5,
		ServerUrl:          "localhost:8080",
		LocalKey:           "",
		UserKey:            "",
		Collectors: CollectorSettings{
			CPU: CPUCollectorSettings{
				CacheDurationSeconds:       3,
				BackgroundSamplingMillis:   500,
				BackgroundFrequencySeconds: 2,
				DirectSamplingMillis:       750,
			},
			System: SystemCollectorSettings{
				CacheDurationSeconds:   3,
				UpdateFrequencySeconds: 2,
			},
			Memory: MemoryCollectorSettings{
				CacheDurationSeconds:   3,
				UpdateFrequencySeconds: 2,
			},
			Disk: DiskCollectorSettings{
				CacheDurationSeconds:   3,
				UpdateFrequencySeconds: 2,
			},
			Network: NetworkCollectorSettings{
				CacheDurationSeconds:   3,
				UpdateFrequencySeconds: 2,
			},
			Docker: DockerCollectorSettings{
				CacheDurationSeconds:   3,
				UpdateFrequencySeconds: 2,
			},
			Process: ProcessCollectorSettings{
				CacheDurationSeconds:   3,
				UpdateFrequencySeconds: 2,
			},
			Service: ServiceCollectorSettings{
				CacheDurationSeconds:   3,
				UpdateFrequencySeconds: 2,
			},
		},
	}
}

func generateSystemKey() (string, error) {
	logger.Info("새로운 시스템 키 생성 시작")

	// 시스템 정보 수집
	hostname, err := os.Hostname()
	if err != nil {
		logger.Error("Hostname 가져오기 실패: " + err.Error())
		return "", fmt.Errorf("failed to get hostname: %w", err)
	}

	// 새로운 키 생성 (랜덤 바이트)
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		logger.Error("랜덤 바이트 생성 실패: " + err.Error())
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// 키 생성을 위한 데이터 조합
	keySource := fmt.Sprintf("%s_%x", hostname, randomBytes)

	logger.Info("새로운 시스템 키 해시 생성 시작")
	// sha512 해시 생성
	hash := sha512.Sum512([]byte(keySource))
	hashedKey := hex.EncodeToString(hash[:])

	return hashedKey, nil
}

// LoadConfig는 설정 파일을 읽어 Config 구조체로 변환합니다
func LoadConfig() (Config, error) {
	// 현재 작업 디렉토리를 기준으로 설정 파일 경로 결정
	wd, err := os.Getwd()
	if err != nil {
		return Config{}, fmt.Errorf("작업 디렉토리를 가져올 수 없습니다: %w", err)
	}
	configDir := filepath.Join(wd, "configs")
	configFile := filepath.Join(configDir, "config.json")

	// 파일 존재 여부 확인
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		logger.Info("설정 파일(config.json)이 없어 새로 생성합니다.")
		config := DefaultConfig()

		newKey, err := generateSystemKey()
		if err != nil {
			return config, fmt.Errorf("새 설정 파일의 시스템 키 생성 실패: %w", err)
		}
		config.LocalKey = newKey

		if err := SaveConfig(config); err != nil {
			return config, fmt.Errorf("새 설정 파일 저장 실패: %w", err)
		}
		return config, nil
	}

	// 파일이 존재하면 읽기
	data, err := os.ReadFile(configFile)
	if err != nil {
		return Config{}, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		// 파싱에 실패하면 기본 설정 반환
		logger.Error("설정 파일 파싱 실패, 기본 설정값을 사용합니다: " + err.Error())
		return DefaultConfig(), nil
	}

	// 키가 없으면 생성하고 저장
	if config.LocalKey == "" {
		newKey, err := generateSystemKey()
		if err != nil {
			return config, fmt.Errorf("failed to generate system key: %w", err)
		}
		config.LocalKey = newKey

		// 새로운 키가 포함된 설정을 저장
		if err := SaveConfig(config); err != nil {
			return config, fmt.Errorf("failed to save new system key: %w", err)
		}
		logger.Info("기존 설정 파일에 키가 없어 새로 생성 후 저장했습니다.")
	}

	return config, nil
}

// SaveConfig는 Config 구조체를 JSON 파일로 저장합니다
func SaveConfig(config Config) error {
	// 현재 작업 디렉토리를 기준으로 설정 파일 경로 결정
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("작업 디렉토리를 가져올 수 없습니다: %w", err)
	}
	configDir := filepath.Join(wd, "configs")

	// configs 디렉토리가 없으면 생성
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	configFile := filepath.Join(configDir, "config.json")

	// Config 구조체를 JSON으로 변환
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	// 파일에 쓰기
	return os.WriteFile(configFile, data, 0644)
}

// LoadConfigFromPath는 지정된 경로의 설정 파일을 읽어 Config 구조체로 변환합니다
func LoadConfigFromPath(configPath string) (Config, error) {
	// 파일 존재 여부 확인
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		logger.Info("설정 파일이 없어 새로 생성합니다: " + configPath)
		config := DefaultConfig()

		newKey, err := generateSystemKey()
		if err != nil {
			return config, fmt.Errorf("새 설정 파일의 시스템 키 생성 실패: %w", err)
		}
		config.LocalKey = newKey

		// 설정 파일 디렉토리 생성
		configDir := filepath.Dir(configPath)
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return config, fmt.Errorf("설정 디렉토리 생성 실패: %w", err)
		}

		// 설정 파일 저장
		data, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return config, fmt.Errorf("설정 JSON 변환 실패: %w", err)
		}
		if err := os.WriteFile(configPath, data, 0644); err != nil {
			return config, fmt.Errorf("설정 파일 저장 실패: %w", err)
		}

		return config, nil
	}

	// 파일이 존재하면 읽기
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		// 파싱에 실패하면 기본 설정 반환
		logger.Error("설정 파일 파싱 실패, 기본 설정값을 사용합니다: " + err.Error())
		return DefaultConfig(), nil
	}

	// 키가 없으면 생성하고 저장
	if config.LocalKey == "" {
		newKey, err := generateSystemKey()
		if err != nil {
			return config, fmt.Errorf("failed to generate system key: %w", err)
		}
		config.LocalKey = newKey

		// 새로운 키가 포함된 설정을 저장
		data, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return config, fmt.Errorf("설정 JSON 변환 실패: %w", err)
		}
		if err := os.WriteFile(configPath, data, 0644); err != nil {
			return config, fmt.Errorf("설정 파일 저장 실패: %w", err)
		}

		logger.Info("기존 설정 파일에 키가 없어 새로 생성 후 저장했습니다.")
	}

	return config, nil
}
