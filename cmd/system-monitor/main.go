package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"system-monitor/internal/config"
	"system-monitor/internal/logger"
	"system-monitor/internal/network/client"
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

	logger.Info("Config loaded: " + config.ServerUrl)
	logger.Info("Collection interval: " + strconv.Itoa(config.CollectionInterval))
	logger.Info("Sending interval: " + strconv.Itoa(config.SendingInterval))
	logger.Info("Local Key: " + config.LocalKey)
	logger.Info("User Key: " + config.UserKey)

	return &config
}

func main() {
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
