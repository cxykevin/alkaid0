//go:build windows

package windows

import "testing"

// TestLibNetUserAddSignature 编译期回归测试：NetUserAdd 的 serverName 参数必须
// 是 LPCWSTR（*uint16）。此前是 *string，非 nil 时传给 API 的是 Go string header
// （数据指针+长度），Windows 会把指针字节当宽字符串读取。
func TestLibNetUserAddSignature(t *testing.T) {
	var addFn func(*uint16, LibNetUserAddLevel, *byte, *uint32) (uint32, error) = LibNetUserAdd
	if addFn == nil {
		t.Fatal("LibNetUserAdd 不可为 nil")
	}
	var setFn func(*uint16, *uint16, uint32, *byte, *uint32) (uint32, error) = LibNetUserSetInfo
	if setFn == nil {
		t.Fatal("LibNetUserSetInfo 不可为 nil")
	}
	if LibNetUserSetInfoLevel1003 != 1003 {
		t.Fatalf("NetUserSetInfo 修改密码的 level 必须是 1003，实际 %d", LibNetUserSetInfoLevel1003)
	}
}
