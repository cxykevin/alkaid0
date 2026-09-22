package connect

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/cxykevin/alkaid0/log"
)

var loggerStdio = log.New("connect(stdio)")

// stdioExit 是退出实现的间接层，仅用于测试（生产代码不应修改）。
var stdioExit = os.Exit

// StartStdio 从 stdio 启动 JSON-RPC
func StartStdio(handler func(string, func(string) error, uint64) (returnString string, exit bool), closeConn func(uint64)) {
	startStdio(os.Stdin, os.Stdout, handler, closeConn)
}

// startStdio 是 StartStdio 的可注入实现：读写流由调用方提供，
// 以便对"一行一条消息"的分帧、空行忽略与 EOF 收尾做直接单测。
func startStdio(in io.Reader, out io.Writer, handler func(string, func(string) error, uint64) (returnString string, exit bool), closeConn func(uint64)) {
	reader := bufio.NewReader(in)
	loggerStdio.Info("connect start(ConnID 1)")

	// 最多 8 个并发消息处理 goroutine
	sem := make(chan struct{}, 8)

	var writeMu sync.Mutex

	for {
		// 从 stdin 读取一行
		line, err := reader.ReadString('\n')
		if line == "" && err != nil {
			// 没有更多数据：正常收尾。
			// 注意 ReadString 在遇到 EOF 时会连同已读到的内容一起返回，因此
			// 不能在 err != nil 时直接 break：客户端写完最后一条消息（末尾没有
			// 换行）就关闭 stdin 时，那条消息会在此前的实现里被丢掉。
			if err != io.EOF {
				loggerStdio.Error("stdio read error: %v", err)
			}
			break
		}

		// 去除空白字符
		line = strings.TrimSpace(line)
		if line != "" {
			// 获取 semaphore slot（backpressure：队列满时阻塞读取）
			sem <- struct{}{}

			go func() {
				defer func() { <-sem }()
				// 调用 handler 处理请求
				responseStr, shouldExit := handler(line, func(t string) error {
					writeMu.Lock()
					defer writeMu.Unlock()
					_, werr := fmt.Fprintln(out, t)
					return werr
				}, 1)

				if responseStr != "" {
					// 将响应写入 stdout
					writeMu.Lock()
					fmt.Fprintln(out, responseStr)
					writeMu.Unlock()
				}

				// 检查是否需要退出
				if shouldExit {
					stdioExit(0)
				}
			}()
		}

		if err != nil {
			// 最后一行（EOF 前没有换行）已交给处理协程，结束读取
			break
		}
	}
	loggerStdio.Info("connect end(ConnID 1)")
	closeConn(1)
}
