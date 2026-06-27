#!/bin/sh
### BEGIN INIT INFO
# Provides:          alkaid0
# Required-Start:    $network $local_fs
# Required-Stop:     $network $local_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: Alkaid0 Coding Agent
# Description:       Alkaid0 Coding Agent daemon
### END INIT INFO

NAME=alkaid0
DAEMON=/usr/bin/alkaid0
PIDFILE=/var/run/alkaid0.pid
DAEMON_ARGS=""

# 设置环境变量
export ALKAID0_LOG_PATH="/var/log/alkaid0/log.log"
export ALKAID0_CONFIG_PATH="/etc/alkaid0/config.json"

test -x $DAEMON || exit 0

. /lib/lsb/init-functions

case "$1" in
    start)
        log_daemon_msg "Starting $NAME"
        start-stop-daemon --start --background --make-pidfile \
            --pidfile $PIDFILE --exec $DAEMON -- $DAEMON_ARGS
        log_end_msg $?
        ;;
    stop)
        log_daemon_msg "Stopping $NAME"
        start-stop-daemon --stop --pidfile $PIDFILE --retry 5
        rm -f $PIDFILE
        log_end_msg $?
        ;;
    restart)
        $0 stop
        sleep 1
        $0 start
        ;;
    status)
        status_of_proc -p $PIDFILE $DAEMON $NAME && exit 0 || exit $?
        ;;

    enable)
        if command -v update-rc.d >/dev/null 2>&1; then
            update-rc.d $NAME defaults >/dev/null 2>&1
        elif command -v chkconfig >/dev/null 2>&1; then
            chkconfig --add $NAME >/dev/null 2>&1
            chkconfig $NAME on >/dev/null 2>&1
        else
            echo "ERROR: No known tool to enable service (update-rc.d or chkconfig)" >&2
            exit 1
        fi
        exit 0
        ;;

    disable)
        if command -v update-rc.d >/dev/null 2>&1; then
            update-rc.d -f $NAME remove >/dev/null 2>&1
        elif command -v chkconfig >/dev/null 2>&1; then
            chkconfig $NAME off >/dev/null 2>&1
            chkconfig --del $NAME >/dev/null 2>&1
        else
            echo "ERROR: No known tool to disable service (update-rc.d or chkconfig)" >&2
            exit 1
        fi
        exit 0
        ;;

    is_enabled)
        if ls /etc/rc?.d/S*$NAME 2>/dev/null | grep -q .; then
            exit 0
        else
            exit 1
        fi
        ;;

    *)
        echo "Usage: $0 {start|stop|restart|status|enable|disable|is_enabled}"
        exit 1
        ;;
esac

exit 0
