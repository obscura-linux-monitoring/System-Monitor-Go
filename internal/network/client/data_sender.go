package client

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"system-monitor/internal/common"
	"system-monitor/internal/logger"
	"system-monitor/internal/models"

	"github.com/gorilla/websocket"
)

// DataSender 데이터 전송 클래스
type DataSender struct {
	serverInfo  ServerInfo
	dataQueue   common.Queue
	userID      string
	conn        *websocket.Conn
	isConnected atomic.Bool
	running     atomic.Bool

	// 고루틴 관련
	senderDone chan struct{}
	clientDone chan struct{}

	// 뮤텍스
	mutex sync.Mutex
}

// NewDataSender 생성자
func NewDataSender(serverInfo ServerInfo, dataQueue common.Queue, userID string) *DataSender {
	return &DataSender{
		serverInfo: serverInfo,
		dataQueue:  dataQueue,
		userID:     userID,
		senderDone: make(chan struct{}),
		clientDone: make(chan struct{}),
	}
}

// Connect 서버에 WebSocket 연결 시도
func (ds *DataSender) Connect() bool {
	// URI 구성
	u := url.URL{
		Scheme: "ws",
		Host:   fmt.Sprintf("%s:%d", ds.serverInfo.Address, ds.serverInfo.Port),
		Path:   "/ws",
	}

	logger.Info("WebSocket 서버에 연결 시도: " + u.String())

	// 웹소켓 연결 시도
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		logger.Error("WebSocket 연결 실패: " + err.Error())
		return false
	}

	ds.conn = conn
	ds.isConnected.Store(true)

	// 메시지 수신 고루틴 시작
	go ds.receiveMessages()

	logger.Info("WebSocket 서버에 연결 성공")
	return true
}

// Disconnect 서버와의 WebSocket 연결 종료
func (ds *DataSender) Disconnect() {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	if ds.isConnected.Load() {
		// 고루틴 종료 신호 전송
		close(ds.clientDone)

		// 연결 종료
		if ds.conn != nil {
			ds.conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "정상 종료"),
				time.Now().Add(time.Second))
			ds.conn.Close()
			ds.conn = nil
		}

		ds.isConnected.Store(false)
		logger.Info("WebSocket 연결이 정상적으로 종료되었습니다.")
	}
}

// StartSending 데이터 전송 작업 시작
func (ds *DataSender) StartSending(intervalSeconds int) {
	if ds.running.Load() {
		return
	}

	ds.running.Store(true)
	go ds.sendLoop(intervalSeconds)
}

// StopSending 데이터 전송 작업 중지
func (ds *DataSender) StopSending() {
	if !ds.running.Load() {
		return
	}

	ds.running.Store(false)
	close(ds.senderDone)
}

// IsConnected 연결 상태 반환
func (ds *DataSender) IsConnected() bool {
	return ds.isConnected.Load()
}

// sendLoop 데이터 전송 루프
func (ds *DataSender) sendLoop(intervalSeconds int) {
	logger.Info("데이터 전송 루프 시작")

	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ds.senderDone:
			logger.Info("데이터 전송 루프 종료")
			return
		case <-ticker.C:
			startTime := time.Now()

			// 큐에서 데이터 가져와서 전송
			metrics, ok := ds.dataQueue.TryPop()
			if ok {
				if metricsData, ok := metrics.(models.SystemMetrics); ok {
					ds.sendMetrics(metricsData)
				}
			} else {
				logger.Info("데이터 큐에서 데이터를 가져오지 못했습니다.")
			}

			elapsed := time.Since(startTime)
			logger.Info(fmt.Sprintf("데이터 전송 루프 소요 시간: %v", elapsed))
		}
	}
}

// sendMetrics 시스템 메트릭 데이터 전송
func (ds *DataSender) sendMetrics(metrics models.SystemMetrics) bool {
	logger.Info("데이터 전송 시작")
	logger.Info("metrics.USER_ID: " + metrics.USER_ID + ", metrics.Key: " + metrics.Key)

	if !ds.isConnected.Load() {
		return false
	}

	// 전송 시작 시간 기록
	sendStartTime := time.Now()

	// 메트릭을 JSON으로 변환
	metricsJson, err := json.Marshal(metrics)
	if err != nil {
		logger.Error("메트릭 JSON 변환 실패: " + err.Error())
		return false
	}

	// WebSocket으로 전송
	ds.mutex.Lock()
	err = ds.conn.WriteMessage(websocket.TextMessage, metricsJson)
	ds.mutex.Unlock()

	// 전송 종료 시간 기록 및 소요 시간 계산
	sendEndTime := time.Now()
	sendDuration := sendEndTime.Sub(sendStartTime)

	// 전송 결과 로깅
	logMessage := fmt.Sprintf("[전송] 시작: %s, 종료: %s, 소요 시간: %v, 데이터 크기: %d바이트",
		sendStartTime.Format("2006-01-02 15:04:05"),
		sendEndTime.Format("2006-01-02 15:04:05"),
		sendDuration,
		len(metricsJson))

	if err != nil {
		logMessage += fmt.Sprintf(", 오류 발생: %s", err.Error())
		logger.Error(logMessage)
		return false
	} else {
		logMessage += ", 성공"
		logger.Info(logMessage)
	}

	logger.Info("데이터 전송 종료")
	return true
}

// receiveMessages 메시지 수신 고루틴
func (ds *DataSender) receiveMessages() {
	logger.Info("receiveMessages")

	for {
		select {
		case <-ds.clientDone:
			logger.Info("receiveMessages clientDone")
			return
		default:
			if ds.conn == nil {
				logger.Info("receiveMessages conn is nil")
				time.Sleep(time.Second)
				continue
			}

			_, message, err := ds.conn.ReadMessage()
			if err != nil {
				logger.Error("receiveMessages err: " + err.Error())
				ds.isConnected.Store(false)
				return
			}

			ds.handleMessage(message)
		}
	}
}

// handleMessage 메시지 처리
func (ds *DataSender) handleMessage(message []byte) {
	logger.Info("handleMessage")

	var response map[string]string
	err := json.Unmarshal(message, &response)
	if err != nil {
		logger.Error("JSON 파싱 오류: " + err.Error())
		return
	}

	typ, hasType := response["type"]
	result, hasResult := response["result"]

	if !hasType || !hasResult {
		logger.Error("응답 포맷 오류: 필수 필드 누락")
		return
	}

	logger.Info(fmt.Sprintf("응답 수신: 타입=%s, 결과=%s", typ, result))

	switch typ {
	case "metrics_response":
		logger.Info("메트릭 전송 응답 수신: " + result)
	case "error":
		logger.Error("오류 응답 수신: " + result)
	default:
		logger.Info("알 수 없는 응답 유형: " + typ)
	}
}
