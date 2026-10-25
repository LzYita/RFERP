package repository

import "app/internal/model"

// bomProductsForPart 列出 BOM 里引用了该零件的产品。
//
// 删除零件时必须先把「被谁用了」说清楚：bom_items.part_id 的外键没有
// ON DELETE 级联，所以数据库本来就会拦下这次删除，但它只会抛一句
// FOREIGN KEY constraint failed —— 用户既不知道是哪个产品，也不知道该去改什么。
//
// (product_id, part_id) 在 bom_items 上有唯一约束，因此一个产品只会出现一次。
func bomProductsForPart(q queryer, partID int64) ([]model.ProductRef, error) {
	list := make([]model.ProductRef, 0)
	err := q.Select(&list, `
		SELECT p.id, p.code, p.name
		FROM bom_items b
		JOIN products p ON p.id = b.product_id
		WHERE b.part_id = ?
		ORDER BY p.code`, partID)
	return list, err
}

// BOMProductsForPart 见 bomProductsForPart。
func (r *Repository) BOMProductsForPart(partID int64) ([]model.ProductRef, error) {
	return bomProductsForPart(r.db, partID)
}

// BOMProductsForPart 见 bomProductsForPart。
func (s *SQLiteStore) BOMProductsForPart(partID int64) ([]model.ProductRef, error) {
	return bomProductsForPart(s.db, partID)
}

// BOMProductsForPart 见 bomProductsForPart。
func (t *Tx) BOMProductsForPart(partID int64) ([]model.ProductRef, error) {
	return bomProductsForPart(t.tx, partID)
}

// BOMProductsForPart 见 bomProductsForPart。
func (t *SQLiteTx) BOMProductsForPart(partID int64) ([]model.ProductRef, error) {
	return bomProductsForPart(t.q, partID)
}
