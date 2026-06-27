#!/bin/sh
APP_NAME="alkaid0"

# 停止并禁用服务
if command -v systemctl > /dev/null 2>&1; then
    systemctl stop "${APP_NAME}"
    systemctl disable "${APP_NAME}"
    rm -f "/usr/lib/systemd/system/${APP_NAME}.service"
    systemctl daemon-reload
elif command -v rc-update > /dev/null 2>&1; then
    rc-service "${APP_NAME}" stop
    rc-update del "${APP_NAME}"
    rm -f "/etc/init.d/${APP_NAME}"
else
    service "${APP_NAME}" stop
    if command -v chkconfig > /dev/null 2>&1; then
        chkconfig --del "${APP_NAME}"
    elif command -v update-rc.d > /dev/null 2>&1; then
        update-rc.d "${APP_NAME}" remove
    fi
    rm -f "/etc/init.d/${APP_NAME}"
fi
