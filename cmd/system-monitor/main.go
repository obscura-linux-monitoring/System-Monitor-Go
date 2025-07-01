package main

import (
	"encoding/json"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"system-monitor/internal/config"
	"system-monitor/internal/logger"
	"system-monitor/internal/network/client"
	"system-monitor/internal/utils"
)

// checkRootPrivileges는 프로그램이 루트 권한으로 실행되고 있는지 확인합니다.
func checkRootPrivileges() bool {
	return syscall.Geteuid() == 0
}

// getConfigPath는 실행 파일이 있는 디렉토리 기준으로 config.json 경로를 반환합니다.
func getConfigPath() string {
	// 실행 파일의 디렉토리 경로 가져오기
	execPath, err := os.Executable()
	if err != nil {
		logger.Error("실행 파일 경로를 가져올 수 없습니다: " + err.Error())
		return ""
	}

	// 실행 파일의 디렉토리
	execDir := filepath.Dir(execPath)

	// config.json 파일 경로
	return filepath.Join(execDir, "configs", "config.json")
}

func loadConfig() *config.Config {
	// 설정 파일 경로 지정
	configPath := getConfigPath()
	if configPath == "" {
		return nil
	}

	// 설정 파일 경로 출력
	logger.Info("설정 파일 경로: " + configPath)

	// 설정 파일 로드
	config, err := config.LoadConfigFromPath(configPath)
	if err != nil {
		logger.Error("Failed to load config: " + err.Error())
		return nil
	}

	// 외부 IP가 없으면 구해서 저장
	if config.ExternalIP == "" {
		logger.Info("외부 IP가 설정되지 않아 조회합니다...")
		externalIP, err := utils.GetExternalIP()
		if err != nil {
			logger.Error("외부 IP 조회 실패: " + err.Error())
			// 외부 IP 조회 실패해도 프로그램은 계속 실행되도록 함
		} else {
			config.ExternalIP = externalIP
			logger.Info("외부 IP 조회 성공: " + externalIP)
			
			// 변경된 설정을 파일에 저장
			if err := saveConfigToPath(configPath, config); err != nil {
				logger.Error("외부 IP 설정 저장 실패: " + err.Error())
			} else {
				logger.Info("외부 IP가 설정 파일에 저장되었습니다")
			}
		}
	} else {
		logger.Info("기존 외부 IP 사용: " + config.ExternalIP)
	}

	logger.Info("Config loaded: " + config.ServerUrl)
	logger.Info("Collection interval: " + strconv.Itoa(config.CollectionInterval))
	logger.Info("Sending interval: " + strconv.Itoa(config.SendingInterval))
	logger.Info("Local Key: " + config.LocalKey)
	logger.Info("User Key: " + config.UserKey)
	logger.Info("External IP: " + config.ExternalIP)

	return &config
}

// saveConfigToPath는 주어진 경로에 Config 구조체를 JSON 파일로 저장합니다
func saveConfigToPath(configPath string, cfg config.Config) error {
	// 설정 파일 디렉토리 생성 (없을 경우)
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	// Config 구조체를 JSON으로 변환
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	// 파일에 쓰기
	return os.WriteFile(configPath, data, 0644)
}

func main() {
	obscuraKey := flag.String("k", "", "Obscura Key")
	flag.Parse()

	// 로그 초기화 (실행 파일 기준 경로 사용)
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		logDir := filepath.Join(execDir, "logs")
		logger.InitLogger(logDir)
	}

	// sudo 권한 확인
	if !checkRootPrivileges() {
		logger.Error("시스템 모니터는 관리자 권한(sudo)으로 실행되어야 합니다")
		return
	}

	appConfig := loadConfig()
	if appConfig == nil {
		logger.Error("Failed to load config, cannot proceed")
		return
	}

	// obscuraKey 값이 있으면 UserKey에 저장하고 설정 파일 업데이트
	if *obscuraKey != "" {
		logger.Info("Obscura Key를 UserKey로 설정합니다")
		appConfig.UserKey = *obscuraKey

		// 변경된 설정을 config.json 파일에 저장
		configPath := getConfigPath()
		if configPath != "" {
			if err := saveConfigToPath(configPath, *appConfig); err != nil {
				logger.Error("설정 파일 업데이트 실패: " + err.Error())
			} else {
				logger.Info("설정 파일 업데이트 완료: UserKey가 저장되었습니다")
			}
		}
	}

	// obscuraKey가 비어 있고 UserKey도 비어 있으면 실행 중지
	if *obscuraKey == "" && appConfig.UserKey == "" {
		logger.Error("Obscura Key가 제공되지 않았습니다")
		return
	}

	logger.Info("Start System Monitor")

	// 1. 시스템 클라이언트 생성
	// serverUrl을 호스트와 포트로 분리
	hostPort := strings.Split(appConfig.ServerUrl, ":")
	serverHost := hostPort[0]
	serverPort := 8080 // 기본값
	if len(hostPort) > 1 {
		port, err := strconv.Atoi(hostPort[1])
		if err == nil {
			serverPort = port
		}
	}

	// ServerInfo 생성
	serverInfo := client.ServerInfo{
		Address: serverHost,
		Port:    serverPort,
	}

	// SystemClient 생성
	systemClient := client.NewSystemClient(
		serverInfo,
		appConfig.LocalKey,
		appConfig.CollectionInterval,
		appConfig.SendingInterval,
		appConfig.UserKey)

	// 클라이언트 초기화 - 여기서 MasterCollector도 생성됨
	err = systemClient.Initialize(*appConfig)
	if err != nil {
		logger.Error("클라이언트 초기화 실패: " + err.Error())
		return
	}

	// 서버 연결 및 데이터 송신 시작 - 여기서 MasterCollector.Start()를 호출함
	connected := systemClient.Connect()
	if !connected {
		logger.Error("서버 연결 실패")
		return
	}

	// Wait for termination signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	// 연결 종료 및 리소스 정리
	systemClient.Disconnect()

	logger.Info("Received signal " + sig.String() + ", shutting down...")
}
