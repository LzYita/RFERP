package api

import (
	"net/http"
	"strconv"

	"app/internal/usecase"
)

// handleStockProjection 返回库存投影：把未完成批次将要消耗的量先扣掉，
// 得到"这些批次都做完之后还剩多少"。
//
// 可选参数 product_id + plan_qty 把一批"还没创建的计划"也并进去，
// 供建批弹窗做预览。两个参数必须成对出现——只给一个多半是调用方写错了，
// 静默忽略会让预览少算一批，比报错更危险。
func (s *Server) handleStockProjection(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rawProduct, rawPlan := q.Get("product_id"), q.Get("plan_qty")

	var in usecase.StockProjectionInput
	if rawProduct != "" || rawPlan != "" {
		if rawProduct == "" || rawPlan == "" {
			writeErr(w, http.StatusBadRequest, "product_id 与 plan_qty 必须同时提供")
			return
		}
		pid, err := strconv.ParseInt(rawProduct, 10, 64)
		if err != nil || pid <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid product_id")
			return
		}
		plan, err := strconv.Atoi(rawPlan)
		if err != nil || plan <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid plan_qty")
			return
		}
		in.ProductID, in.PlanQty = pid, plan
	}

	out, err := s.apps.StockProjection(in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
