package main

import (
	"errors"
	"testing"

	"app/internal/bootstrap"
)

// 回退询问的措辞分类：本机数据库/服务连不上时，要说清"回退通常也解决不了"。
//
// 它只影响措辞、不影响是否提供回退 —— 这条边界也要守住：分类错了顶多多一句
// 提醒，不能让用户失去回退入口。所以这里只断言"提示有/无"，不断言"是否询问"。

func TestRollbackHintForConnectionFailure(t *testing.T) {
	err := &bootstrap.ConnectionError{
		EnsureErr:  errors.New("start service: access denied"),
		ConnectErr: errors.New("连接数据库: dial tcp 127.0.0.1:3306: connectex: No connection could be made"),
	}
	got := rollbackUnrelatedHint(err)
	if got == "" {
		t.Fatal("数据库连不上时应当提示「回退通常也不能解决」，否则用户会白退一次")
	}
}

// 包了两层也不能漏：bootstrap 会把连接错误再包一层 StageError。
func TestRollbackHintSurvivesWrapping(t *testing.T) {
	inner := &bootstrap.ConnectionError{ConnectErr: errors.New("boom")}
	wrapped := errors.Join(errors.New("bootstrap failed"), inner)

	if rollbackUnrelatedHint(wrapped) == "" {
		t.Error("被包装过的连接错误没被识别")
	}
}

// 普通错误（配置损坏、端口被占、程序本身崩）不该说"与更新无关"：
// 那些情况回退往往真的有用，误导用户反而有害。
func TestRollbackHintSilentForOtherFailures(t *testing.T) {
	if got := rollbackUnrelatedHint(errors.New("配置文件损坏")); got != "" {
		t.Errorf("与数据库无关的故障不该加这句提示，实际 %q", got)
	}
	if got := rollbackUnrelatedHint(nil); got != "" {
		t.Errorf("nil 错误不该加提示，实际 %q", got)
	}
}
