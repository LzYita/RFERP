package service

import (
	"app/internal/model"
	"app/internal/repository"
	"errors"
	"reflect"
	"testing"
)

type traceQueryStore struct {
	repository.Store
	query string
	part  bool
	err   error
}

func (s *traceQueryStore) TraceByProduct(q string) ([]model.BatchTrace, error) {
	s.query = q
	return []model.BatchTrace{{ID: 7}}, s.err
}
func (s *traceQueryStore) TraceByPart(q string) ([]model.BatchTrace, error) {
	s.query = q
	s.part = true
	return []model.BatchTrace{{ID: 7}}, s.err
}
func TestTraceQueryValidationAndErrors(t *testing.T) {
	for _, part := range []bool{false, true} {
		store := &traceQueryStore{}
		svc := NewWithSnapshot(store, nil, nil)
		query := svc.TraceByProduct
		if part {
			query = svc.TraceByPart
		}
		if _, err := query(" \t"); err == nil || store.query != "" {
			t.Fatal("blank input reached store")
		}
		rows, err := query("  exact-code  ")
		if err != nil || !reflect.DeepEqual(rows, []model.BatchTrace{{ID: 7}}) || store.query != "exact-code" || store.part != part {
			t.Fatalf("dispatch rows=%v err=%v store=%+v", rows, err, store)
		}
		cause := errors.New("query failed")
		store.err = cause
		if _, err := query("code"); !errors.Is(err, cause) {
			t.Fatalf("query error=%v", err)
		}
	}
}
