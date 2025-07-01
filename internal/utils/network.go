package utils

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"system-monitor/internal/logger"
)

// ExternalIPServices는 외부 IP를 조회할 수 있는 다양한 서비스들입니다.
var ExternalIPServices = []string{
	"https://ipv4.icanhazip.com",
	"https://api.ipify.org",
	"https://checkip.amazonaws.com",
	"https://ipinfo.io/ip",
	"https://ifconfig.me/ip",
}

// GetExternalIP는 여러 외부 서비스를 통해 공인 IP 주소를 가져옵니다.
func GetExternalIP() (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	var lastErr error
	
	for _, service := range ExternalIPServices {
		logger.Info("외부 IP 조회 시도: " + service)
		
		resp, err := client.Get(service)
		if err != nil {
			logger.Error("외부 IP 서비스 연결 실패 (" + service + "): " + err.Error())
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logger.Error("외부 IP 서비스 응답 오류 (" + service + "): HTTP " + resp.Status)
			lastErr = fmt.Errorf("HTTP %s", resp.Status)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.Error("외부 IP 응답 읽기 실패 (" + service + "): " + err.Error())
			lastErr = err
			continue
		}

		ip := strings.TrimSpace(string(body))
		if ip != "" && isValidIP(ip) {
			logger.Info("외부 IP 조회 성공: " + ip + " (from " + service + ")")
			return ip, nil
		}

		logger.Error("외부 IP 형식 오류 (" + service + "): " + ip)
		lastErr = fmt.Errorf("invalid IP format: %s", ip)
	}

	return "", fmt.Errorf("모든 외부 IP 서비스 조회 실패: %w", lastErr)
}

// isValidIP는 간단한 IP 주소 형식 검증을 수행합니다.
func isValidIP(ip string) bool {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return false
	}
	
	for _, part := range parts {
		if len(part) == 0 || len(part) > 3 {
			return false
		}
		// 숫자만 포함하는지 간단 체크
		for _, c := range part {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
} 