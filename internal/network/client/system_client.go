package client

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"system-monitor/internal/collectors"
	"system-monitor/internal/config"
	"system-monitor/internal/logger"
)

// ServerInfo 서버 연결 정보
type ServerInfo struct {
	Address string
	Port    int
}

// SystemClient 시스템 클라이언트 구조체
type SystemClient struct {
	serverInfo         ServerInfo
	systemKey          string
	userID             string
	collectionInterval int
	sendingInterval    int
	collectorManager   *collectors.MasterCollector
	dataSender         *DataSender
	isDisconnected     atomic.Bool
	mutex              sync.Mutex
}

// NewSystemClient SystemClient 생성자
func NewSystemClient(serverInfo ServerInfo, systemKey string, collectionInterval, sendingInterval int, userID string) *SystemClient {
	// 시작 시간 기록
	startTime := time.Now()
	logger.Info("시스템 클라이언트 초기화 시작: " + startTime.Format("2006-01-02 15:04:05"))

	// 클라이언트 객체 생성
	client := &SystemClient{
		serverInfo:         serverInfo,
		systemKey:          systemKey,
		userID:             userID,
		collectionInterval: collectionInterval,
		sendingInterval:    sendingInterval,
	}

	// 종료 시간 기록 및 소요 시간 출력
	endTime := time.Now()
	initDuration := endTime.Sub(startTime)
	logger.Info("시스템 클라이언트 초기화 완료: " + endTime.Format("2006-01-02 15:04:05") +
		", 소요 시간: " + initDuration.String())

	return client
}

// Initialize 클라이언트 초기화
func (c *SystemClient) Initialize(cfg interface{}) error {
	// 타입 어설션으로 config.Config 타입 변환
	configData, ok := cfg.(config.Config)
	if !ok {
		return fmt.Errorf("잘못된 설정 타입: config.Config가 필요합니다")
	}

	// MasterCollector 초기화
	c.collectorManager = collectors.NewMasterCollector(configData)

	// DataSender 초기화
	queue := c.collectorManager.GetDataQueue()
	c.dataSender = NewDataSender(c.serverInfo, queue, c.userID)

	return nil
}

// Connect 서버에 연결하고 데이터 수집 및 전송 시작
func (c *SystemClient) Connect() bool {
	// 시작 시간 기록
	connectStartTime := time.Now()
	logger.Info("시스템 클라이언트 연결 시작: " + connectStartTime.Format("2006-01-02 15:04:05"))

	// 수집 시작
	c.collectorManager.Start(c.collectionInterval)

	// 서버 연결 및 송신 시작
	connected := false
	if c.dataSender.Connect() {
		c.dataSender.StartSending(c.sendingInterval)
		connected = true
	}

	// 종료 시간 기록 및 소요 시간 출력
	connectEndTime := time.Now()
	connectDuration := connectEndTime.Sub(connectStartTime)

	status := "성공"
	if !connected {
		status = "실패"
	}

	logger.Info("시스템 클라이언트 연결 " + status + ": " +
		connectEndTime.Format("2006-01-02 15:04:05") +
		", 소요 시간: " + connectDuration.String())

	return connected
}

// Disconnect 서버 연결을 종료하고 데이터 수집 및 전송 중지
func (c *SystemClient) Disconnect() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.isDisconnected.Load() {
		return
	}

	c.isDisconnected.Store(true)

	// 데이터 송신 중지
	if c.dataSender != nil {
		c.dataSender.StopSending()
	}

	// 수집 중지
	if c.collectorManager != nil {
		c.collectorManager.Stop()
	}

	// 연결 해제
	if c.dataSender != nil {
		c.dataSender.Disconnect()
	}

	logger.Info("시스템 클라이언트가 정상적으로 종료되었습니다.")
}

// IsConnected 서버 연결 상태 확인
func (c *SystemClient) IsConnected() bool {
	return c.dataSender != nil && c.dataSender.IsConnected()
}
