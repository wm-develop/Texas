package room

import (
	"strings"
	"testing"
)

// SELECT 列数与 Scan 目标数错配不会被内存仓储的单元测试发现，只会在真实
// 数据库上以 "expected N destination arguments in Scan" 暴露——曾导致上线后
// 所有房间读取失败（创建房间后立刻被踢回大厅，之后一律 internal_error）。
func TestMemberSelectColumnsMatchScanTargets(t *testing.T) {
	columns := 0
	for _, column := range strings.Split(memberSelectColumns, ",") {
		if strings.TrimSpace(column) != "" {
			columns++
		}
	}
	var member Member
	if targets := len(memberScanTargets(&member)); targets != columns {
		t.Fatalf("member query selects %d columns but scans into %d targets", columns, targets)
	}
}

func TestRoomSelectColumnsMatchScanTargets(t *testing.T) {
	columns := 0
	for _, column := range strings.Split(roomSelectColumns, ",") {
		if strings.TrimSpace(column) != "" {
			columns++
		}
	}
	var value Room
	var revision int64
	if targets := len(roomScanTargets(&value, &revision)); targets != columns {
		t.Fatalf("loadRoom selects %d columns but scans into %d targets", columns, targets)
	}
}
