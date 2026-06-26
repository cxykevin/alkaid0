#!/bin/sh
APP_NAME="alkaid0"
SERVICE_DIR="/usr/share/${APP_NAME}/services"

# 1. 检测 systemd
if command -v systemctl > /dev/null 2>&1; then
    echo "Detected systemd, installing service..."
    
    # 复制并重命名为标准 .service 名
    cp "${SERVICE_DIR}/systemd.service" "/usr/lib/systemd/system/${APP_NAME}.service"
    
    systemctl daemon-reload
    systemctl enable "${APP_NAME}"
    systemctl start "${APP_NAME}"

# 2. 检测 OpenRC
elif command -v rc-update > /dev/null 2>&1; then
    echo "Detected OpenRC, installing service..."
    
    cp "${SERVICE_DIR}/openrc.sh" "/etc/init.d/${APP_NAME}"
    chmod +x "/etc/init.d/${APP_NAME}"  # OpenRC 脚本需要可执行权限
    
    rc-update add "${APP_NAME}" default
    rc-service "${APP_NAME}" start

# 3. 回退到 SysV init
else
    echo "Detected SysV init, installing service..."
    
    cp "${SERVICE_DIR}/sysv.sh" "/etc/init.d/${APP_NAME}"
    chmod +x "/etc/init.d/${APP_NAME}"   # SysV 脚本也需要可执行权限
    
    # 根据发行版尝试启用
    if command -v chkconfig > /dev/null 2>&1; then
        chkconfig --add "${APP_NAME}"
        service "${APP_NAME}" start
    elif command -v update-rc.d > /dev/null 2>&1; then
        update-rc.d "${APP_NAME}" defaults
        invoke-rc.d "${APP_NAME}" start
    fi
fi
