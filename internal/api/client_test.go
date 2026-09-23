package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientLoginAndListParts(t *testing.T) {
	apps := &fakeApps{loginUser: testWarehouseUser()}
	s := New(apps)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	c := NewClient(ts.URL)
	u, err := c.Login("wh", "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if u.Username != "wh" || c.Token() == "" {
		t.Fatalf("login user=%+v token empty=%v", u, c.Token() == "")
	}
	if _, err := c.ListParts(); err != nil {
		t.Fatalf("list parts: %v", err)
	}
	if err := c.StockIn(stockInput()); err != nil {
		t.Fatalf("stock in: %v", err)
	}
	if apps.stockIn.Operator == "" {
		t.Fatal("expected session operator on server side")
	}
}

func TestClientErrorOnForbidden(t *testing.T) {
	apps := &fakeApps{loginUser: testViewerUser()}
	s := New(apps)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	c := NewClient(ts.URL)
	if _, err := c.Login("v", "pw"); err != nil {
		t.Fatalf("login: %v", err)
	}
	err := c.StockIn(stockInput())
	if err == nil {
		t.Fatal("viewer stock-in must fail")
	}
	ae, ok := err.(*APIError)
	if !ok || ae.Status != http.StatusForbidden {
		t.Fatalf("err=%v, want 403 APIError", err)
	}
}
