package service

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"app/internal/model"
)

func TestWriteAuditReturnsSerializationError(t *testing.T) {
	svc := New(nil, "", "", nil)
	err := svc.writeAudit(auditEntry{
		TableName: "parts",
		RecordID:  1,
		Action:    "INSERT",
		NewData:   map[string]any{"invalid": func() {}},
	})
	if err == nil {
		t.Fatal("writeAudit accepted an unserializable audit payload")
	}
}

func TestCreateProductRollsBackWhenAuditWriteFails(t *testing.T) {
	svc, mock := newMockService(t)
	operator := "operator"
	p := &model.Product{Code: "P-1", Name: "Product", Unit: "个", Status: 1, Operator: &operator}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO products (code,name,spec,unit,status,operator) VALUES (?,?,?,?,?,?)")).
		WithArgs(p.Code, p.Name, p.Spec, p.Unit, p.Status, p.Operator).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(auditInsert).WillReturnError(errors.New("audit unavailable"))
	mock.ExpectRollback()

	if _, err := svc.CreateProduct(p); err == nil {
		t.Fatal("CreateProduct swallowed audit failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}
