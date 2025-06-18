package main

import (
	"flag"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"system-monitor/internal/config"
	"system-monitor/internal/logger"
)

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

func parseArgs() {
	// 명령줄 인자 정의
	serverAddr := flag.String("s", "localhost:8080", "서버 주소 (host:port)")
	countFlag := flag.Int("c", 1, "요청 횟수")
	timeoutFlag := flag.Int("t", 5, "타임아웃 (초)")
	keyFlag := flag.String("k", "", "인증 키")

	// 인자 파싱
	flag.Parse()

	logger.Info("Server address: " + *serverAddr)
	logger.Info("Count: " + strconv.Itoa(*countFlag))
	logger.Info("Timeout: " + strconv.Itoa(*timeoutFlag))
	logger.Info("Key length: " + strconv.Itoa(len(*keyFlag)))
}

func main() {
	appConfig := loadConfig()
	if appConfig == nil {
		logger.Error("Failed to load config, cannot proceed with tests")
		return
	}
	parseArgs()

	logger.Info("Start System Monitor")

	// Wait for termination signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logger.Info("Received signal " + sig.String() + ", shutting down...")
}
