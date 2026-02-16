//go:build windows

package windows

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Pipe 创建带 ACL 的命名管道，返回与 os.Pipe 相同的类型
func Pipe() (*os.File, *os.File, error) {
	dacl, err := GetDACL()
	if err != nil {
		return nil, nil, fmt.Errorf("get DACL: %v", err)
	}

	// 生成唯一管道名
	name := fmt.Sprintf(`\\.\pipe\alk_sandbox_%d_%d`, windows.GetCurrentProcessId(), getTickCount())

	// 创建安全描述符
	sa, err := getSecurityDescriptor(dacl)
	if err != nil {
		return nil, nil, fmt.Errorf("create security descriptor: %v", err)
	}

	// 创建命名管道（服务端）
	handle, err := createNamedPipe(name, sa)
	// // 安全描述符内存释放：CreateNamedPipe 已复制内容，现在可以释放
	// windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(sa.lpSecurityDescriptor))))
	if err != nil {
		return nil, nil, fmt.Errorf("create named pipe: %v", err)
	}

	// 客户端连接
	clientHandle, err := connectClient(name)
	if err != nil {
		windows.CloseHandle(handle)
		return nil, nil, fmt.Errorf("connect client: %v", err)
	}

	// 服务端接受连接
	err = connectServer(handle)
	if err != nil {
		windows.CloseHandle(handle)
		windows.CloseHandle(clientHandle)
		return nil, nil, fmt.Errorf("connect server: %v", err)
	}

	// 转换为 *os.File
	// 服务端 handle 作为读端，客户端 clientHandle 作为写端
	r, err := handleToFile(handle, name)
	if err != nil {
		windows.CloseHandle(handle)
		windows.CloseHandle(clientHandle)
		return nil, nil, err
	}

	w, err := handleToFile(clientHandle, name)
	if err != nil {
		r.Close() // handle 已被关闭
		windows.CloseHandle(clientHandle)
		return nil, nil, err
	}

	return r, w, nil
}

// createNamedPipe 创建命名管道服务端
func createNamedPipe(name string, sa *windows.SecurityAttributes) (windows.Handle, error) {
	namePtr, _ := windows.UTF16PtrFromString(name)

	handle, err := windows.CreateNamedPipe(
		namePtr,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1,    // 最大实例数
		1024, // 输出缓冲区
		1024, // 输入缓冲区
		0,    // 默认超时
		sa,
	)
	if err != nil {
		return 0, err
	}
	return handle, nil
}

// connectClient 客户端连接命名管道
func connectClient(name string) (windows.Handle, error) {
	namePtr, _ := windows.UTF16PtrFromString(name)

	// 循环尝试连接（等待服务端创建）
	var handle windows.Handle
	var err error
	for range 100 {
		handle, err = windows.CreateFile(
			namePtr,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0, // 不共享
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED,
			0,
		)
		if err == nil {
			break
		}
		// 等待一下重试
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		return 0, err
	}

	// 设置管道模式
	var mode uint32 = windows.PIPE_READMODE_BYTE
	err = windows.SetNamedPipeHandleState(handle, &mode, nil, nil)
	if err != nil {
		windows.CloseHandle(handle)
		return 0, err
	}

	return handle, nil
}

// connectServer 服务端接受连接
func connectServer(handle windows.Handle) error {
	// 使用重叠 I/O 实现异步连接
	overlapped := windows.Overlapped{}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	overlapped.HEvent = event

	err = windows.ConnectNamedPipe(handle, &overlapped)
	if err == windows.ERROR_IO_PENDING {
		// 等待连接完成（实际上客户端已连接，会立即完成）
		_, err = windows.WaitForSingleObject(event, 1000)
		if err != nil {
			return err
		}
	} else if err != nil && err != windows.ERROR_PIPE_CONNECTED {
		return err
	}
	return nil
}

// handleToFile 将 Windows handle 转换为 *os.File
func handleToFile(handle windows.Handle, name string) (*os.File, error) {
	// 复制句柄，避免所有权问题
	var newHandle windows.Handle
	currentProcess := windows.CurrentProcess()
	err := windows.DuplicateHandle(
		currentProcess,
		handle,
		currentProcess,
		&newHandle,
		0,
		false,
		windows.DUPLICATE_SAME_ACCESS,
	)
	if err != nil {
		return nil, fmt.Errorf("duplicate handle: %v", err)
	}
	windows.CloseHandle(handle)

	// 创建 os.NewFile，现在 *os.File 拥有 newHandle 的所有权
	file := os.NewFile(uintptr(newHandle), name)
	if file == nil {
		windows.CloseHandle(newHandle)
		return nil, fmt.Errorf("os.NewFile failed")
	}

	return file, nil
}

// getTickCount 简单计数器
var tickCounter uint32

func getTickCount() uint32 {
	tickCounter++
	return tickCounter
}
