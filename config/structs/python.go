package structs

// PythonConfig 配置全局 IPython 虚拟环境。
type PythonConfig struct {
	// Path 指定用于创建虚拟环境的 Python 可执行文件；为空时自动查找 python3/python。
	Path string
	// Source 指定 pip 安装源；为空时使用 pip 默认源。
	Source string
}
