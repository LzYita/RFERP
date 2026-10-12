package repository

import (
	"errors"
	"strings"
	"testing"
)

// 唯一键冲突以前把驱动原文拼在后面，用户看到的是
// 「唯一键冲突: UNIQUE constraint failed: parts.code」—— 英文驱动串对用户没有意义。
func TestTranslateUniqueToChinese(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want string
	}{
		{
			"SQLite 零件编码重复",
			errors.New("UNIQUE constraint failed: parts.code"),
			"唯一键冲突: 零件编码已存在",
		},
		{
			"MySQL 零件编码重复",
			errors.New("Error 1062 (23000): Duplicate entry 'P-001' for key 'parts.code'"),
			"唯一键冲突: 零件编码已存在",
		},
		{
			"SQLite BOM 重复零件",
			errors.New("UNIQUE constraint failed: bom_items.product_id, bom_items.part_id"),
			"唯一键冲突: 该产品BOM中已包含此零件",
		},
		{
			"未识别的约束保留原文，不丢信息",
			errors.New("UNIQUE constraint failed: users.username"),
			"唯一键冲突: UNIQUE constraint failed: users.username",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := TranslateError(c.in)
			if !errors.Is(err, ErrUniqueConflict) {
				t.Fatalf("未识别为唯一键冲突：%v", err)
			}
			if err.Error() != c.want {
				t.Errorf("got %q\nwant %q", err.Error(), c.want)
			}
			if strings.Contains(err.Error(), "UNIQUE constraint") &&
				strings.Contains(c.want, "UNIQUE constraint") == false {
				t.Errorf("英文驱动串泄漏到用户可见文案：%q", err.Error())
			}
		})
	}
}

// 非唯一约束错误必须原样透传，不能被误判。
func TestTranslatePassesThroughOthers(t *testing.T) {
	in := errors.New("no such table: parts")
	if got := TranslateError(in); got.Error() != in.Error() {
		t.Errorf("got %q, want 原样透传 %q", got.Error(), in.Error())
	}
	if TranslateError(nil) != nil {
		t.Error("nil 应返回 nil")
	}
	// 已翻译过的错误不应被二次包装
	once := TranslateError(errors.New("UNIQUE constraint failed: parts.code"))
	twice := TranslateError(once)
	if twice.Error() != once.Error() {
		t.Errorf("二次翻译改动了文案：%q -> %q", once.Error(), twice.Error())
	}
}
