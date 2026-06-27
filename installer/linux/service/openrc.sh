#!/sbin/openrc-run

name="Alkaid0 Coding Agent"
description="Alkaid0 Coding Agent daemon"
command="/usr/bin/alkaid0"
command_args=""
pidfile="/run/alkaid0.pid"
command_background=true

# 设置环境变量
export ALKAID0_LOG_PATH="/var/log/alkaid0/log.log"
export ALKAID0_CONFIG_PATH="/etc/alkaid0/config.json"

depend() {
    need net
    after net
}
