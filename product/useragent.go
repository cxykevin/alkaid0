package product

import (
	"runtime"
	"strings"
)

// UserAgentTemplate UA 字符串模板
const UserAgentTemplate = "Alkaid0/{version} ({system} {sysArch}) Go/{goVersion}"

// UserAgent UA 字符串
var UserAgent = strings.ReplaceAll(
	strings.ReplaceAll(
		strings.ReplaceAll(
			strings.ReplaceAll(
				UserAgentTemplate,
				"{version}", Version),
			"{system}", runtime.GOOS),
		"{sysArch}", runtime.GOARCH),
	"{goVersion}", runtime.Version())
