package configutil

import "os"

// ExpandPath 展开路径中的 ~ 和环境变量
func ExpandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		// 获取用户家目录
		homeDir, err := os.UserHomeDir()
		if err == nil {
			path = homeDir + path[1:]
		}
	}
	// 展开环境变量
	return os.ExpandEnv(path)
}
