package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

// 공개 로거 인스턴스
var Logger zerolog.Logger

func init() {
	// 시간 포맷 설정 (밀리초 표시)
	zerolog.TimeFieldFormat = time.RFC3339Nano

	// 로그 디렉토리 생성
	logDir := "logs"
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		_ = os.MkdirAll(logDir, 0775)
	}

	// 로그 파일 이름 (프로그램 시작 시간 기준)
	logFile := fmt.Sprintf("%s.log", time.Now().Format("2006-01-02"))
	logPath := filepath.Join(logDir, logFile)

	// 파일 로거 설정 (lumberjack으로 로테이션)
	fileWriter := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    5, // 5MB
		MaxBackups: 10,
		MaxAge:     14, // 14일간 보관
		Compress:   true,
	}

	// 콘솔 출력 포맷 설정 (개발 환경용)
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "2006-01-02 15:04:05.000",
	}

	// 파일과 콘솔에 동시 출력
	multiWriter := io.MultiWriter(consoleWriter, fileWriter)

	// 파일, 함수, 라인 정보 추가 (전체 경로 대신 파일명만 표시)
	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		return filepath.Base(file) + ":" + strconv.Itoa(line)
	}

	// 로거 설정
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	Logger = zerolog.New(multiWriter).
		With().Timestamp().CallerWithSkipFrameCount(3).Logger()
}

// 로깅 헬퍼 함수들
func Info(msg string) {
	Logger.Info().Msg(msg)
}

func Error(msg string) {
	Logger.Error().Msg(msg)
}

func Debug(msg string) {
	Logger.Debug().Msg(msg)
}

func Warn(msg string) {
	Logger.Warn().Msg(msg)
}

// InitLogger는 지정된 로그 디렉토리에 로거를 초기화합니다
func InitLogger(logDir string) {
	// 먼저 로그 디렉토리 생성 및 권한 설정
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		_ = os.MkdirAll(logDir, 0775)
	}

	// 로그 파일 이름
	logFile := fmt.Sprintf("%s.log", time.Now().Format("2006-01-02"))
	logPath := filepath.Join(logDir, logFile)

	// 중요: lumberjack 로거 설정 전에 빈 파일을 먼저 생성하고 권한 설정
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		emptyFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0664)
		if err == nil {
			emptyFile.Close()
		}
	}

	// 파일 소유권 변경 (이 부분을 lumberjack 설정 전에 수행)
	if sudoUID := os.Getenv("SUDO_UID"); sudoUID != "" {
		if uid, err := strconv.Atoi(sudoUID); err == nil {
			gid := -1
			if sudoGID := os.Getenv("SUDO_GID"); sudoGID != "" {
				gid, _ = strconv.Atoi(sudoGID)
			}
			os.Chown(logPath, uid, gid)
			os.Chown(logDir, uid, gid)
		}
	}

	// 파일 로거 설정
	fileWriter := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    5,
		MaxBackups: 10,
		MaxAge:     14,
		Compress:   true,
	}

	// 콘솔 출력 포맷 설정 (개발 환경용)
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "2006-01-02 15:04:05.000",
	}

	// 파일과 콘솔에 동시 출력
	multiWriter := io.MultiWriter(consoleWriter, fileWriter)

	// 파일, 함수, 라인 정보 추가 (전체 경로 대신 파일명만 표시)
	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		return filepath.Base(file) + ":" + strconv.Itoa(line)
	}

	// 로거 설정
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	Logger = zerolog.New(multiWriter).
		With().Timestamp().CallerWithSkipFrameCount(3).Logger()

	Info("로그 시스템 초기화 완료: " + logPath)
}
