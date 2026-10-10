package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"app/internal/model"
)

// HTTP 层的请求 DTO。
//
// 存在理由：model 里的 Status 是普通 int，而 JSON 解码无法区分
// 「客户端没传这个字段」与「客户端明确传了 0」。两者落到 int 上都是 0，
// 于是漏传一次就把零件静默建成停用件，且没有任何报错。
//
// 这不是假设：迁移到 Web UI 时新客户端很容易漏传，Fyne 侧一直显式传 1
// 所以从未暴露。用指针在协议层保留「未提供」这个信息，创建时补默认值，
// 更新时沿用原值——两种情况下都不会静默改变零件的启用状态。

// 零件/产品的合法状态取值。界面只区分「启用」与「停用」，
// 其余取值一律视为停用（见 ui.partStatusStyle），所以这里明确拒绝，
// 避免 3、99 之类脏数据悄悄流进来。
const (
	statusEnabled  = 1
	statusDisabled = 2
)

func validStatus(v int) bool {
	return v == statusEnabled || v == statusDisabled
}

// partWriteReq 是零件的创建与更新请求体。
type partWriteReq struct {
	Code     string   `json:"code"`
	Name     string   `json:"name"`
	Spec     *string  `json:"spec"`
	Unit     string   `json:"unit"`
	PartType *string  `json:"part_type"`
	StockQty float64  `json:"stock_qty"`
	WarnQty  float64  `json:"warn_qty"`
	Status   *int     `json:"status"`
	Supplier *string  `json:"supplier"`
	ID       int64    `json:"id,omitempty"`
	Version  int      `json:"version"`
	Operator *string  `json:"-"`
	_        struct{} `json:"-"`
}

func decodePartWrite(w http.ResponseWriter, r *http.Request) (*partWriteReq, bool) {
	var in partWriteReq
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return nil, false
	}
	if in.Status != nil && !validStatus(*in.Status) {
		writeErr(w, http.StatusBadRequest,
			fmt.Sprintf("status 只能是 %d（启用）或 %d（停用）", statusEnabled, statusDisabled))
		return nil, false
	}
	return &in, true
}

// toPartForCreate 转成新建零件。status 未提供时补「启用」。
func (in *partWriteReq) toPartForCreate() *model.Part {
	p := &model.Part{
		Code:     in.Code,
		Name:     in.Name,
		Spec:     in.Spec,
		Unit:     in.Unit,
		PartType: in.PartType,
		StockQty: in.StockQty,
		WarnQty:  in.WarnQty,
		Supplier: in.Supplier,
		Operator: in.Operator,
		Status:   statusEnabled,
	}
	if in.Status != nil {
		p.Status = *in.Status
	}
	return p
}

// toPartForUpdate 转成更新零件。
//
// status 未提供时沿用库中原值：更新语义是「改这几项」而不是「整行替换」，
// 把漏传当成「改成停用」是最坏的解读——用户只是改了名字，零件却消失了。
func (in *partWriteReq) toPartForUpdate(s *Server) (*model.Part, error) {
	status := statusEnabled
	switch {
	case in.Status != nil:
		status = *in.Status
	default:
		cur, err := s.apps.GetPart(in.ID)
		if err != nil {
			return nil, fmt.Errorf("读取零件当前状态: %w", err)
		}
		if cur == nil {
			return nil, errors.New("part not found")
		}
		status = cur.Status
	}
	return &model.Part{
		ID:       in.ID,
		Code:     in.Code,
		Name:     in.Name,
		Spec:     in.Spec,
		Unit:     in.Unit,
		PartType: in.PartType,
		StockQty: in.StockQty,
		WarnQty:  in.WarnQty,
		Supplier: in.Supplier,
		Operator: in.Operator,
		Status:   status,
		Version:  in.Version,
	}, nil
}

// productWriteReq 是产品的创建与更新请求体，字段与零件对齐。
type productWriteReq struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Spec     *string `json:"spec"`
	Unit     string  `json:"unit"`
	Status   *int    `json:"status"`
	ID       int64   `json:"id,omitempty"`
	Version  int     `json:"version"`
	Operator *string `json:"-"`
}

func decodeProductWrite(w http.ResponseWriter, r *http.Request) (*productWriteReq, bool) {
	var in productWriteReq
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return nil, false
	}
	if in.Status != nil && !validStatus(*in.Status) {
		writeErr(w, http.StatusBadRequest,
			fmt.Sprintf("status 只能是 %d（启用）或 %d（停用）", statusEnabled, statusDisabled))
		return nil, false
	}
	return &in, true
}

func (in *productWriteReq) toProductForCreate() *model.Product {
	p := &model.Product{
		Code:     in.Code,
		Name:     in.Name,
		Spec:     in.Spec,
		Unit:     in.Unit,
		Operator: in.Operator,
		Status:   statusEnabled,
	}
	if in.Status != nil {
		p.Status = *in.Status
	}
	return p
}

func (in *productWriteReq) toProductForUpdate(s *Server) (*model.Product, error) {
	status := statusEnabled
	switch {
	case in.Status != nil:
		status = *in.Status
	default:
		cur, err := s.apps.GetProduct(in.ID)
		if err != nil {
			return nil, fmt.Errorf("读取产品当前状态: %w", err)
		}
		if cur == nil {
			return nil, errors.New("product not found")
		}
		status = cur.Status
	}
	return &model.Product{
		ID:       in.ID,
		Code:     in.Code,
		Name:     in.Name,
		Spec:     in.Spec,
		Unit:     in.Unit,
		Operator: in.Operator,
		Status:   status,
		Version:  in.Version,
	}, nil
}
