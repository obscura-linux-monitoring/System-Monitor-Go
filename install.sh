#!/bin/bash

# 인자 확인
if [ $# -eq 0 ]; then
    echo "사용법: $0 <obscura_key>"
    echo "예시: $0 1234567890"
    exit 1
fi

OBSCURA_KEY="$1"
echo "Obscura Key: $OBSCURA_KEY"

# 설치 경로
INSTALL_PATH="/opt/system-monitor"
# 정리 함수 정의
cleanup() {
    echo "설치 실패로 인한 정리 작업을 수행합니다..."
    
    # 서비스 중지 및 비활성화 시도
    systemctl stop system-monitor 2>/dev/null
    systemctl disable system-monitor 2>/dev/null
    
    # 서비스 파일 제거
    rm -f /etc/systemd/system/system-monitor.service
    
    # 데몬 리로드
    systemctl daemon-reload
    
    # 심볼릭 링크 제거
    rm -f /usr/local/bin/uninstall-system-monitor
    
    # 설치 디렉토리 정리
    if [ -d "$INSTALL_PATH" ]; then
        rm -rf "$INSTALL_PATH"/*
    fi
    
    echo "정리 완료. 설치를 다시 시도하세요."
    exit 1
}

# 1. sudo 권한 확인
if [ $(id -u) -ne 0 ]; then
    echo "Please run as root"
    exit 1
fi

# 2. os 확인
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$NAME
    OS_ID=$ID
    VERSION=$VERSION_ID
else
    echo "OS 정보를 찾을 수 없습니다."
    exit 1
fi
echo "OS: $OS"
echo "OS ID: $OS_ID"
echo "VERSION: $VERSION"

# 3. 아키텍처 확인
ARCH=$(uname -m)
if [ "$ARCH" == "x86_64" ]; then
    ARCH="amd64"
elif [ "$ARCH" == "aarch64" ]; then
    ARCH="arm64"
else
    echo "Unsupported architecture: $ARCH"
    exit 1
fi
echo "ARCH: $ARCH"

# 4.1 이미 설치 되어 있는지 확인
if systemctl is-active system-monitor > /dev/null 2>&1; then
    echo "System Monitor is already installed"
    exit 1
fi

# 4.2 폴더 생성
mkdir -p $INSTALL_PATH
# 디렉토리 변경 성공 여부 확인
if ! cd $INSTALL_PATH; then
    echo "디렉토리 변경 실패: $INSTALL_PATH"
    exit 1
fi
rm -rf *

# 4.3 dmidecode 설치
if [[ "$OS_ID" == *"ubuntu"* ]] || [[ "$OS_ID" == *"debian"* ]]; then
    apt-get update
    if ! apt-get install -y dmidecode; then
        echo "dmidecode 설치 실패"
        exit 1
    fi
elif [[ "$OS_ID" == *"centos"* ]] || [[ "$OS_ID" == *"rhel"* ]] || [[ "$OS_ID" == *"fedora"* ]]; then
    if [[ "$OS_ID" == *"fedora"* ]]; then
        if ! dnf install -y dmidecode; then
            echo "dmidecode 설치 실패"
            exit 1
        fi
    else
        if ! yum install -y dmidecode; then
            echo "dmidecode 설치 실패"
            exit 1
        fi
    fi
else
    echo "Unsupported OS: $OS ($OS_ID)"
    exit 1
fi

# 5. 다운로드 URL
DOWNLOAD_URL="https://github.com/obscura-linux-monitoring/System-Monitor-Go/releases/latest/download"
echo "DOWNLOAD_URL: $DOWNLOAD_URL"

# 6. opt 폴더에 다운로드
BINARY_NAME="system-monitor-${ARCH}.exec"
if ! wget $DOWNLOAD_URL/$BINARY_NAME -O $INSTALL_PATH/$BINARY_NAME; then
    echo "다운로드 실패: $DOWNLOAD_URL/$BINARY_NAME"
    cleanup
fi

# 6.1 다운로드 파일 확인
if [ ! -f $INSTALL_PATH/$BINARY_NAME ]; then
    echo "Download failed"
    cleanup
fi

# 7. 실행 권한 부여
chmod +x $INSTALL_PATH/$BINARY_NAME

# 8. 서비스 파일 생성
cat <<EOF > /etc/systemd/system/system-monitor.service
[Unit]
Description=System Monitor
After=network.target

[Service]
Environment="OBSCURA_KEY=$OBSCURA_KEY"
ExecStart=/opt/system-monitor/$BINARY_NAME -k $OBSCURA_KEY
Restart=always

[Install]
WantedBy=multi-user.target
EOF

# 9. 서비스 파일 확인
if [ ! -f /etc/systemd/system/system-monitor.service ]; then
    echo "Service file creation failed"
    cleanup
fi

# 10. 서비스 파일 로드
systemctl daemon-reload

# 11. 서비스 활성화
if ! systemctl enable system-monitor; then
    echo "서비스 활성화 실패"
    cleanup
fi

# 12. 서비스 시작
if ! systemctl start system-monitor; then
    echo "서비스 시작 실패"
    systemctl status system-monitor
    cleanup
fi

# uninstall.sh 스크립트 생성
echo "uninstall.sh 스크립트 생성 중..."
if ! cat <<EOF > $INSTALL_PATH/uninstall.sh
#!/bin/bash

# 설치 경로
INSTALL_PATH="/opt/system-monitor"

# 1. sudo 권한 확인
if [ \$(id -u) -ne 0 ]; then
    echo "Please run as root"
    exit 1
fi

echo "System Monitor 제거를 시작합니다..."

# 2. 서비스가 설치되어 있는지 확인
if ! systemctl list-unit-files | grep -q system-monitor.service; then
    echo "System Monitor가 설치되어 있지 않습니다."
    exit 1
fi

# 3. 서비스 중지
echo "서비스 중지 중..."
systemctl stop system-monitor

# 4. 서비스 비활성화
echo "서비스 비활성화 중..."
systemctl disable system-monitor

# 5. 서비스 파일 제거
echo "서비스 파일 제거 중..."
rm -f /etc/systemd/system/system-monitor.service

# 6. systemd 데몬 리로드
systemctl daemon-reload

# 7. 심볼릭 링크 제거
echo "심볼릭 링크 제거 중..."
rm -f /usr/local/bin/uninstall-system-monitor

# 8. 설치 디렉토리 제거
echo "설치 디렉토리 제거 중..."
if [ -d "\$INSTALL_PATH" ]; then
    rm -rf "\$INSTALL_PATH"
fi

echo "System Monitor가 성공적으로 제거되었습니다."
EOF
then
    echo "uninstall.sh 스크립트 생성 실패"
    cleanup
fi

# uninstall.sh에 실행 권한 부여
if ! chmod +x $INSTALL_PATH/uninstall.sh; then
    echo "uninstall.sh 실행 권한 부여 실패"
    cleanup
fi

# 심볼릭 링크 생성
if ! ln -sf $INSTALL_PATH/uninstall.sh /usr/local/bin/uninstall-system-monitor; then
    echo "심볼릭 링크 생성 실패"
    cleanup
fi

echo "제거 스크립트가 $INSTALL_PATH/uninstall.sh에 생성되었습니다."
echo "또는 'uninstall-system-monitor' 명령으로 제거할 수 있습니다."

# 13. 서비스 상태 확인
systemctl status system-monitor

echo "System Monitor 설치가 성공적으로 완료되었습니다!"