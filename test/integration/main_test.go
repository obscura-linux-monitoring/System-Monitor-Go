package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"system-monitor/internal/collectors"
	"system-monitor/internal/config"
	"system-monitor/internal/logger"
	"system-monitor/internal/network/client"
)

// clearLogFile clears the log file contents before the test
func clearLogFile() {
	logDir := "logs"
	logFile := fmt.Sprintf("%s.log", time.Now().Format("2006-01-02"))
	logPath := filepath.Join(logDir, logFile)

	// 파일이 존재하면 내용을 지우거나 삭제
	if _, err := os.Stat(logPath); err == nil {
		err := os.Truncate(logPath, 0)
		if err != nil {
			// 파일을 비우는 데 실패하면 삭제 시도
			os.Remove(logPath)
		}
	}
}

func loadConfig() *config.Config {
	config, err := config.LoadConfig()
	if err != nil {
		logger.Error("Failed to load config: " + err.Error())
		return nil
	}

	logger.Info("Config loaded: " + config.ServerUrl)
	logger.Info("Collection interval: " + strconv.Itoa(config.CollectionInterval))
	logger.Info("Sending interval: " + strconv.Itoa(config.SendingInterval))
	logger.Info("Local Key: " + config.LocalKey)

	return &config
}

func TestMain(t *testing.T) {
	clearLogFile() // 테스트 시작 전에 로그 파일 내용을 지움

	// 설정 로드
	appConfig := loadConfig()
	if appConfig == nil {
		t.Fatal("Failed to load config, cannot proceed with tests")
	}
	logger.Info("Test Start")

	// 1. 시스템 메트릭 수집 테스트
	// MasterCollector를 생성하면 내부적으로 DiskCollector가 생성되고,
	// DiskCollector는 백그라운드에서 주기적인 수집을 시작합니다.
	masterCollector := collectors.NewMasterCollector(*appConfig)

	// 실제 운영 환경과 같이, 백그라운드 수집기가 데이터를 계산하고 캐시할 때까지 기다립니다.
	// 설정된 수집 주기(CollectionInterval)만큼 2번을 기다려,
	// per-second 메트릭이 계산될 시간을 충분히 확보합니다.
	waitDuration := time.Duration(appConfig.CollectionInterval) * time.Second
	logger.Info(fmt.Sprintf("Waiting for 2 collection cycles (%s each)...", waitDuration))
	time.Sleep(waitDuration)

	// 이제 메트릭을 수집합니다.
	// DiskCollector는 백그라운드에서 수집/계산한 최신 결과를 캐시에서 반환합니다.
	logger.Info("Performing collection...")
	metrics, err := masterCollector.CollectAll(appConfig.UserKey, appConfig.LocalKey)
	if err != nil {
		t.Fatal("Failed to collect metrics: " + err.Error())
	}
	logger.Info("Metrics collected")

	jsonData, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal metrics to JSON: %v", err)
	}

	err = os.WriteFile("metrics.json", jsonData, 0644)
	if err != nil {
		t.Fatalf("Failed to write metrics to metrics.json: %v", err)
	}
	logger.Info("Successfully saved metrics to metrics.json")

	// 2. 시스템 클라이언트 전송 테스트
	logger.Info("전송 테스트 시작")

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

	// 클라이언트 초기화
	err = systemClient.Initialize(*appConfig)
	if err != nil {
		t.Fatalf("클라이언트 초기화 실패: %v", err)
	}

	// 서버 연결 및 데이터 송신 시작
	connected := systemClient.Connect()
	if !connected {
		// 실패해도 테스트를 중단하지 않고 로그만 남김
		logger.Warn("서버 연결 실패 - 이 부분은 CI 환경에서 예상됨. 테스트 계속 진행")
	} else {
		// 연결 성공한 경우에만 데이터 전송 테스트 실행
		logger.Info("10초 동안 데이터 전송 테스트 실행...")

		// 완료 신호용 채널 생성
		done := make(chan bool)

		// 고루틴으로 10초 타이머 실행
		go func() {
			timer := time.NewTimer(10 * time.Second)
			<-timer.C
			done <- true
		}()

		// 타이머 완료 대기
		<-done

		// 연결 종료
		systemClient.Disconnect()

		logger.Info("전송 테스트 종료")
	}

	logger.Info("Test End")
}
