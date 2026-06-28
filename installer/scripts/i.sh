#!/bin/bash
# Alkaid0 自动安装脚本（按需配置服务）
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# 所有日志输出到 stderr，避免污染 stdout
log_main() { echo -e "${GREEN}==>${NC} $1" >&2; }
log_sub()  { echo -e "  ${BLUE}-->${NC} $1" >&2; }
log_subwarn()  { echo -e "  ${YELLOW}--> $1${NC}" >&2; }
log_warn() { echo -e "${YELLOW}==> ${RED}警告: $1${NC}" >&2; }
log_error(){ echo -e "${RED}==> 错误: $1${NC}" >&2; exit 1; }

print_logo() {
    cat << 'EOF'
[0m       [47m  [0m [47m  [0m            [46m [0m[46m [0m     [47m  [0m       
       [47m  [0m [47m  [0m                   [47m  [0m [47m      [0m
[47m[8malkaid[0m[8m0[47m  [0m [47m  [0m  [47m  [0m [47m      [0m [47m  [0m [47m   [0m [47m  [0m [47m  [0m  [47m  [0m
[47m  [0m  [47m  [0m [47m  [0m [47m    [0m   [47m  [0m  [47m  [0m [47m  [0m [47m  [0m  [47m  [0m [47m  [0m  [47m  [0m
[47m   [0m [47m     [0m [47m  [0m  [47m  [0m [47m   [0m [47m     [0m [47m      [0m [47m      [0m
[0m  [2m╭────────────────────────────────╮[0m
[0m  [2m│ [0m[1;34malkaid0[0m[2m coding agent installer │[0m
[0m  [2m╰────────────────────────────────╯[0m
EOF
}

detect_pkg_manager() {
    if command -v apt &> /dev/null || command -v apt-get &> /dev/null; then echo "apt"
    elif command -v dnf &> /dev/null; then echo "dnf"
    elif command -v yum &> /dev/null; then echo "yum"
    elif command -v pacman &> /dev/null; then echo "pacman"
    elif command -v apk &> /dev/null; then echo "apk"
    else echo "none"; fi
}

install_dependencies() {
    local missing=()
    command -v curl &> /dev/null || missing+=("curl")
    command -v jq &> /dev/null || missing+=("jq")
    [ ${#missing[@]} -eq 0 ] && return 0

    log_main "缺失依赖: ${missing[*]}，尝试自动安装..."
    local pkg_manager=$(detect_pkg_manager)
    [ "$pkg_manager" = "none" ] && log_error "未检测到包管理器，请手动安装 curl 和 jq"

    local sudo_cmd=""
    [ "$EUID" -ne 0 ] && command -v sudo &> /dev/null && sudo_cmd="sudo"
    [ "$EUID" -ne 0 ] && [ -z "$sudo_cmd" ] && log_error "需要 root 权限，但未找到 sudo"

    case "$pkg_manager" in
        apt)  $sudo_cmd apt update -y && $sudo_cmd apt install -y curl jq ;;
        dnf)  $sudo_cmd dnf install -y curl jq ;;
        yum)  $sudo_cmd yum install -y curl jq ;;
        pacman) $sudo_cmd pacman -S --noconfirm curl jq ;;
        apk)  $sudo_cmd apk add curl jq ;;
        *) log_error "不支持的包管理器: $pkg_manager" ;;
    esac

    command -v curl &> /dev/null && command -v jq &> /dev/null || log_error "依赖安装失败，请手动安装"
    log_main "依赖安装完成"
}

detect_arch() {
    local arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) log_error "不支持的架构: $arch" ;;
    esac
}

detect_service_manager() {
    if command -v systemctl &> /dev/null; then echo "systemd"
    elif [ -d /etc/init.d ] && command -v update-rc.d &> /dev/null; then echo "sysvinit"
    elif command -v rc-update &> /dev/null; then echo "openrc"
    else echo "none"; fi
}

get_latest_release() {
    local api_url="https://api.github.com/repos/cxykevin/alkaid0/releases"
    local max_retries=3
    local retry_delay=5
    local attempt=1
    local tag=""

    while [ $attempt -le $max_retries ]; do
        # 发起请求，静默模式，带超时
        local response
        response=$(curl -s --connect-timeout 10 --max-time 30 "$api_url" 2>/dev/null)

        # 检查是否成功获取响应
        if [ -z "$response" ]; then
            log_subwarn "请求失败 (尝试 $attempt/$max_retries)，${retry_delay}秒后重试..."
        else
            # 检查 API 是否返回错误消息（如限速、资源不存在等）
            if echo "$response" | grep -q '"message":'; then
                local err_msg
                err_msg=$(echo "$response" | jq -r '.message' 2>/dev/null || echo "未知错误")
                log_subwarn "API返回错误: $err_msg (尝试 $attempt/$max_retries)"
            else
                # 尝试解析 tag
                tag=$(echo "$response" | jq -r '.[0] | .tag_name' 2>/dev/null)
                if [ -n "$tag" ] && [ "$tag" != "null" ]; then
                    echo "$tag"
                    return 0
                else
                    log_subwarn "未找到有效的 Release tag (尝试 $attempt/$max_retries)"
                fi
            fi
        fi

        # 未成功且未达到最大次数，等待后重试
        if [ $attempt -lt $max_retries ]; then
            sleep $retry_delay
        fi
        attempt=$((attempt + 1))
    done

    log_error "获取最新 Release 失败，已重试 $max_retries 次"
}

select_package() {
    local arch=$1
    local pkg_manager=$2
    local tag=$3
    local base_url="https://github.com/cxykevin/alkaid0/releases/download/$tag"
    
    # 硬编码支持架构检查
    if [ "$arch" != "amd64" ] && [ "$arch" != "arm64" ]; then
        log_error "不支持的架构: $arch (仅支持 amd64 和 arm64)"
    fi
    
    # 根据包管理器确定唯一的包名
    local package=""
    case "$pkg_manager" in
        apt|apt-get)
            package="alkaid0-linux-${arch}.deb"
            ;;
        dnf|yum)
            package="alkaid0-linux-${arch}.rpm"
            ;;
        pacman)
            package="alkaid0-linux-${arch}.pkg.tar.zst"
            ;;
        apk)
            package="alkaid0-linux-${arch}.apk"
            ;;
        *)
            log_error "不支持的包管理器: $pkg_manager (仅支持 apt, dnf, yum, pacman, apk)"
            ;;
    esac
    
    local url="${base_url}/${package}"
    local max_retries=3
    local retry_delay=1
    local attempt=1
    local http_code=""
    
    while [ $attempt -le $max_retries ]; do
        log_sub "检查: $url (尝试 $attempt/$max_retries)"
        http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 --max-time 10 -I "$url" 2>/dev/null || echo "000")
        if [ "$http_code" = "200" ] || [ "$http_code" = "302" ] || [ "$http_code" = "301" ]; then
            echo "$package"
            return 0
        fi
        if [ $attempt -lt $max_retries ]; then
            sleep $retry_delay
        fi
        attempt=$((attempt + 1))
    done
    
    log_error "包 $package 在 $max_retries 次尝试后仍不可用"
}

download_and_install() {
    local arch=$1
    local pkg_manager=$2
    local tag=$3
    local package=$4
    local download_url="https://github.com/cxykevin/alkaid0/releases/download/$tag/$package"
    local temp_dir="/tmp/alkaid0_install"
    
    mkdir -p "$temp_dir"
    cd "$temp_dir"
    log_sub "下载: $package"
    curl -L --connect-timeout 10 --max-time 120 -o "$package" "$download_url" || log_error "下载失败"
    
    case "$package" in
        *.deb)
            log_sub "安装 .deb 包"
            sudo dpkg -i "$package" || sudo apt-get install -f -y
            ;;
        *.rpm)
            log_sub "安装 .rpm 包"
            if command -v dnf &> /dev/null; then sudo dnf localinstall -y "$package"
            else sudo rpm -ivh "$package"; fi
            ;;
        *.pkg.tar.zst)
            log_sub "安装 Arch 包"
            sudo pacman -U --noconfirm "$package"
            ;;
        *.apk)
            log_sub "安装 Alpine 包"
            sudo apk add --allow-untrusted "$package"
            ;;
        *)
            log_sub "安装 binary 到 /usr/bin/alkaid0"
            sudo cp "$package" /usr/bin/alkaid0
            sudo chmod +x /usr/bin/alkaid0
            ;;
    esac
    cd /tmp
    rm -rf "$temp_dir"
}

setup_service() {
    local service_manager=$1
    case "$service_manager" in
        systemd)
            log_sub "配置 systemd"
            sudo curl -L -o /etc/systemd/system/alkaid0.service \
                "https://github.com/cxykevin/alkaid0/raw/refs/heads/main/installer/linux/service/systemd.service"
            sudo systemctl daemon-reload && sudo systemctl enable alkaid0 && sudo systemctl start alkaid0
            ;;
        sysvinit)
            log_sub "配置 sysvinit"
            sudo curl -L -o /etc/init.d/alkaid0 \
                "https://github.com/cxykevin/alkaid0/raw/refs/heads/main/installer/linux/service/sysv.sh"
            sudo chmod +x /etc/init.d/alkaid0
            command -v update-rc.d &> /dev/null && sudo update-rc.d alkaid0 defaults
            command -v chkconfig &> /dev/null && { sudo chkconfig --add alkaid0; sudo chkconfig alkaid0 on; }
            sudo /etc/init.d/alkaid0 start
            ;;
        openrc)
            log_sub "配置 openrc"
            sudo curl -L -o /etc/init.d/alkaid0 \
                "https://github.com/cxykevin/alkaid0/raw/refs/heads/main/installer/linux/service/openrc.sh"
            sudo chmod +x /etc/init.d/alkaid0
            sudo rc-update add alkaid0 default && sudo rc-service alkaid0 start
            ;;
        *) log_warn "未检测到支持的服务管理器，请手动配置" ;;
    esac
}

main() {
    print_logo
    log_main "Alkaid0 安装脚本"
    install_dependencies
    ARCH=$(detect_arch)
    PKG_MANAGER=$(detect_pkg_manager)
    SERVICE_MANAGER=$(detect_service_manager)
    log_sub "架构: $ARCH"
    log_sub "包管理器: $PKG_MANAGER"
    log_sub "服务管理器: $SERVICE_MANAGER"
    
    log_main "获取最新 Release..."
    TAG=$(get_latest_release)
    log_sub "最新版本: $TAG"
    
    PACKAGE=$(select_package "$ARCH" "$PKG_MANAGER" "$TAG")
    log_sub "选择安装包: $PACKAGE"
    
    # 判断是否为包管理器格式
    if [[ "$PACKAGE" =~ \.(deb|rpm|pkg\.tar\.zst|apk)$ ]]; then
        PKG_INSTALL=true
    else
        PKG_INSTALL=false
    fi
    
    log_main "下载并安装"
    download_and_install "$ARCH" "$PKG_MANAGER" "$TAG" "$PACKAGE"
    
    # 服务配置：仅当使用 binary 安装时才执行
    if [ "$PKG_INSTALL" = false ]; then
        if [ "$SERVICE_MANAGER" != "none" ]; then
            log_main "配置服务（binary 安装）"
            setup_service "$SERVICE_MANAGER"
        else
            log_warn "未检测到服务管理器，跳过服务配置"
            log_sub "手动运行: /usr/bin/alkaid0"
        fi
    fi
    
    log_main "安装完成!"
    log_sub "配置文件: /etc/alkaid0/config.json"
    log_sub "日志文件: /var/log/alkaid0/log.log"
}

main