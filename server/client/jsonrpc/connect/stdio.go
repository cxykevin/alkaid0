package connect

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cxykevin/alkaid0/log"
)

var loggerStdio = log.New("connect(stdio)")

// StartStdio 从 stdio 启动 JSON-RPC
func StartStdio(handler func(string, func(string) error, uint64) (returnString string, exit bool), closeConn func(uint64)) {
	reader := bufio.NewReader(os.Stdin)
	loggerStdio.Info("connect start(ConnID 1)")

	for {
		// 从 stdin 读取一行
		line, err := reader.ReadString('\n')
		if err != nil {
			// 读取错误时退出
			if err == io.EOF {
				break
			}
			return
		}

		// 去除空白字符
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		go func() {
			// 调用 handler 处理请求
			responseStr, shouldExit := handler(line, func(t string) error {
				fmt.Println(t)
				return nil
			}, 1)

			if responseStr != "" {
				// 将响应写入 stdout
				fmt.Println(responseStr)
			}

			// 检查是否需要退出
			if shouldExit {
				os.Exit(0)
			}
		}()
	}
	loggerStdio.Info("connect end(ConnID 1)")
	closeConn(1)
}
