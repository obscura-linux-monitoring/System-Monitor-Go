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

// CommandResult 명령 실행 결과 구조체
type CommandResult struct {
	CommandID     string `json:"command_id"`
	NodeID        string `json:"node_id"`
	CommandType   string `json:"command_type"`
	CommandStatus bool   `json:"command_status"`
	ResultStatus  bool   `json:"result_status"`
	ResultMessage string `json:"result_message"`
	Target        string `json:"target"`
}

// DataSender 데이터 전송 클래스
type DataSender struct {
	serverInfo         ServerInfo
	dataQueue          common.Queue
	commandResultQueue common.Queue
	commandQueue       common.Queue
	userID             string
	conn               *websocket.Conn
	isConnected        atomic.Bool
	running            atomic.Bool
	numWorkers         int

	// 고루틴 관련
	senderDone  chan struct{}
	clientDone  chan struct{}
	commandDone chan struct{}
	workerDone  chan struct{}

	// 뮤텍스
	mutex sync.Mutex
}

// NewDataSender 생성자
func NewDataSender(serverInfo ServerInfo, dataQueue common.Queue, userID string) *DataSender {
	return &DataSender{
		serverInfo:         serverInfo,
		dataQueue:          dataQueue,
		commandResultQueue: common.NewQueue(100),
		commandQueue:       common.NewQueue(100),
		userID:             userID,
		numWorkers:         4,
		senderDone:         make(chan struct{}),
		clientDone:         make(chan struct{}),
		commandDone:        make(chan struct{}),
		workerDone:         make(chan struct{}),
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

	// 명령 처리 고루틴 시작
	go ds.commandProcessor()

	// 워커 고루틴 시작
	for i := 0; i < ds.numWorkers; i++ {
		go ds.workerThread(i)
	}

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
		close(ds.commandDone)
		close(ds.workerDone)

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

	// JSON 파싱 후 명령 처리 결과 추가
	var metricsObj map[string]interface{}
	err = json.Unmarshal(metricsJson, &metricsObj)
	if err != nil {
		logger.Error("JSON 파싱 실패: " + err.Error())
		return false
	}

	// 커맨드 결과 큐에서 데이터 가져오기
	var commandResults []CommandResult
	for {
		result, ok := ds.commandResultQueue.TryPop()
		if !ok {
			break
		}
		if cmd, ok := result.(CommandResult); ok {
			commandResults = append(commandResults, cmd)
		}
	}

	// 커맨드 결과가 있으면 JSON에 추가
	if len(commandResults) > 0 {
		metricsObj["command_results"] = commandResults
	}

	// 최종 JSON을 문자열로 변환
	data, err := json.Marshal(metricsObj)
	if err != nil {
		logger.Error("JSON 최종 변환 실패: " + err.Error())
		return false
	}

	// WebSocket으로 전송
	ds.mutex.Lock()
	err = ds.conn.WriteMessage(websocket.TextMessage, data)
	ds.mutex.Unlock()

	// 전송 종료 시간 기록 및 소요 시간 계산
	sendEndTime := time.Now()
	sendDuration := sendEndTime.Sub(sendStartTime)

	// 전송 결과 로깅
	logMessage := fmt.Sprintf("[전송] 시작: %s, 종료: %s, 소요 시간: %v, 데이터 크기: %d바이트",
		sendStartTime.Format("2006-01-02 15:04:05"),
		sendEndTime.Format("2006-01-02 15:04:05"),
		sendDuration,
		len(data))

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
	for {
		select {
		case <-ds.clientDone:
			return
		default:
			if ds.conn == nil {
				time.Sleep(time.Second)
				continue
			}

			_, message, err := ds.conn.ReadMessage()
			if err != nil {
				logger.Error("메시지 수신 오류: " + err.Error())
				ds.isConnected.Store(false)
				return
			}

			ds.handleMessage(message)
		}
	}
}

// handleMessage 메시지 처리
func (ds *DataSender) handleMessage(message []byte) {
	logger.Info("메시지 수신: " + string(message))

	var messageJson map[string]interface{}
	err := json.Unmarshal(message, &messageJson)
	if err != nil {
		logger.Error("JSON 파싱 오류: " + err.Error())
		return
	}

	// 커맨드 목록 확인
	commandsJson, ok := messageJson["commands"].([]interface{})
	if !ok {
		return
	}

	// 파싱된 커맨드를 처리 큐에 추가
	for _, cmdInterface := range commandsJson {
		cmd, ok := cmdInterface.(map[string]interface{})
		if !ok {
			continue
		}

		command := CommandResult{}

		if val, ok := cmd["CommandID"].(string); ok {
			command.CommandID = val
		}
		if val, ok := cmd["NodeID"].(string); ok {
			command.NodeID = val
		}
		if val, ok := cmd["CommandType"].(string); ok {
			command.CommandType = val
		}
		if val, ok := cmd["CommandStatus"].(bool); ok {
			command.CommandStatus = val
		}
		if val, ok := cmd["Target"].(string); ok {
			command.Target = val
		}

		// 수신한 커맨드 정보 로깅
		logger.Info(fmt.Sprintf("커맨드 수신: ID=%s, 타입=%s, 대상=%s",
			command.CommandID, command.CommandType, command.Target))

		// 비동기적 처리를 위해 커맨드를 작업 큐에 추가
		ds.commandQueue.Push(command)
	}
}

// commandProcessor 커맨드 처리 고루틴
func (ds *DataSender) commandProcessor() {
	logger.Info("커맨드 처리기 시작")

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ds.commandDone:
			logger.Info("커맨드 처리기 종료")
			return
		case <-ticker.C:
			// 주기적으로 큐 상태 확인 및 보고
			logger.Debug(fmt.Sprintf("커맨드 처리 상태: 대기 중인 작업=%d, 완료된 결과=%d",
				ds.commandQueue.Size(), ds.commandResultQueue.Size()))
		}
	}
}

// processCommand 커맨드를 처리하는 함수
func (ds *DataSender) processCommand(command CommandResult) CommandResult {
	// 실제 명령 처리 코드는 여기에 구현
	// 지금은 단순히 성공 결과를 반환
	result := command
	result.ResultStatus = true
	result.ResultMessage = "명령이 성공적으로 처리되었습니다."

	return result
}

// workerThread 워커 고루틴
func (ds *DataSender) workerThread(workerID int) {
	logger.Info(fmt.Sprintf("워커 %d 시작", workerID))

	for {
		select {
		case <-ds.workerDone:
			logger.Info(fmt.Sprintf("워커 %d 종료", workerID))
			return
		default:
			// 커맨드 큐에서 처리할 작업 가져오기
			cmdInterface, ok := ds.commandQueue.TryPop()
			if !ok {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			command, ok := cmdInterface.(CommandResult)
			if !ok {
				continue
			}

			// 커맨드 처리 시작 로깅
			logger.Info(fmt.Sprintf("워커 %d: 커맨드 처리 시작: ID=%s, 타입=%s",
				workerID, command.CommandID, command.CommandType))

			// 커맨드 처리 및 결과 저장
			result := ds.processCommand(command)

			// 처리 결과를 결과 큐에 저장
			ds.commandResultQueue.Push(result)

			status := "성공"
			if !result.ResultStatus {
				status = "실패"
			}

			logger.Info(fmt.Sprintf("워커 %d: 커맨드 처리 완료: ID=%s, 결과=%s",
				workerID, result.CommandID, status))
		}
	}
}
