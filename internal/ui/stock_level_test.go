package ui

import (
	"testing"

	"app/internal/model"
)

// 库存档位判定。必须与 Web 版 stockLevel（web/src/lib/lowStock.ts）逐字一致，
// 那边一改这里就得跟着改，否则两版对「什么算没货」的说法又分叉。
func TestStockLevel(t *testing.T) {
	cases := []struct {
		name string
		part model.Part
		want int
	}{
		{"库存充足", model.Part{StockQty: 100, WarnQty: 10}, stockOK},
		{"跌破预警线", model.Part{StockQty: 2, WarnQty: 10}, stockLow},
		{"正好等于预警线不算低", model.Part{StockQty: 8000, WarnQty: 8000}, stockOK},
		// 关键用例：warn_qty=0 的含义是「不预警」。少了守卫，判定会退化成
		// stock<=0，于是所有未设预警线的零件都被算成断货。
		{"库存为 0 且未设预警线仍是零库存", model.Part{StockQty: 0, WarnQty: 0}, stockOut},
		{"有货且未设预警线不算低", model.Part{StockQty: 12, WarnQty: 0}, stockOK},
		{"负库存（异常数据）算零库存", model.Part{StockQty: -5, WarnQty: 10}, stockOut},
		// 零库存优先于跌破预警线：两者都要提醒，但红色比橙色更硬。
		{"零库存优先于跌破预警线", model.Part{StockQty: 0, WarnQty: 10}, stockOut},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stockLevel(c.part); got != c.want {
				t.Fatalf("stockLevel(%+v) = %d, want %d", c.part, got, c.want)
			}
		})
	}
}

// 预警口径包含零库存：这一条决定了工作台报的数字和零件页筛出来的条数一致。
func TestIsStockAlert(t *testing.T) {
	alert := []model.Part{
		{StockQty: 0, WarnQty: 0},  // 未设预警线但没货 —— 要报
		{StockQty: 0, WarnQty: 10}, // 没货 —— 要报
		{StockQty: 2, WarnQty: 10}, // 跌破预警线 —— 要报
	}
	for _, p := range alert {
		if !isStockAlert(p) {
			t.Errorf("isStockAlert(%+v) = false, want true", p)
		}
	}
	quiet := []model.Part{
		{StockQty: 12, WarnQty: 0},      // 有货、未设预警线 —— 不报
		{StockQty: 8000, WarnQty: 8000}, // 正好等于预警线 —— 不报
		{StockQty: 100, WarnQty: 10},    // 库存充足 —— 不报
	}
	for _, p := range quiet {
		if isStockAlert(p) {
			t.Errorf("isStockAlert(%+v) = true, want false", p)
		}
	}
}

// 零库存与跌破预警线必须用不同颜色：两者处置动作不同，混成一个信号
// 用户就分不出「先补货」还是「先下单」。
func TestPartStatusStyleDistinctsOutFromLow(t *testing.T) {
	_, outFg, outText := partStatusStyle(stockOut, 1)
	_, lowFg, lowText := partStatusStyle(stockLow, 1)
	if outText != "无库存" || lowText != "库存紧张" {
		t.Fatalf("文案不对：out=%q low=%q", outText, lowText)
	}
	if outFg == lowFg {
		t.Fatal("零库存与低库存用了同一种前景色，用户无法区分")
	}
	// 两档都优先于启停：没货的停用件仍要显示库存问题。
	if _, _, text := partStatusStyle(stockOut, 2); text != "无库存" {
		t.Errorf("停用件有库存问题时应显示库存档位，得到 %q", text)
	}
	if _, _, text := partStatusStyle(stockOK, 2); text != "停用" {
		t.Errorf("正常库存的停用件应显示停用，得到 %q", text)
	}
	if _, _, text := partStatusStyle(stockOK, 1); text != "启用" {
		t.Errorf("正常库存的启用件应显示启用，得到 %q", text)
	}
}
