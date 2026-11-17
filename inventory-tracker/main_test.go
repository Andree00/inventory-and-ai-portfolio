package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Run against a fresh, disposable MySQL database using INVENTORY_TEST_DSN.
// The test creates and modifies records and must never target real user data.
func TestInventoryWorkflow(t *testing.T) {
	dsn := os.Getenv("INVENTORY_TEST_DSN")
	if dsn == "" {
		t.Skip("Set INVENTORY_TEST_DSN to a fresh disposable MySQL database")
	}
	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Ping(); err != nil {
		t.Fatal(err)
	}
	var existing int
	if err = db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing != 0 {
		t.Fatal("test database must be empty")
	}
	if err = migrate(); err != nil {
		t.Fatal(err)
	}
	if err = migrateDocuments(); err != nil {
		t.Fatal(err)
	}
	if err = migrateParties(); err != nil {
		t.Fatal(err)
	}
	call := func(fn func(http.ResponseWriter, *http.Request) error, method string, body any, want int) {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/", bytes.NewReader(data))
		rec := httptest.NewRecorder()
		handler(fn)(rec, req)
		if rec.Code != want {
			t.Fatalf("%s: got %d, want %d: %s", method, rec.Code, want, rec.Body.String())
		}
	}
	call(lookup("brands"), "POST", M{"name": "Brand"}, 200)
	call(parties("customers"), "POST", M{"name": "Example customer", "phone": "123"}, 200)
	call(parties("vendors"), "POST", M{"name": "Example vendor"}, 200)
	call(parties("vendors"), "PUT", M{"id": 1, "name": "Updated vendor", "email": "vendor@example.test"}, 200)
	call(parties("vendors"), "POST", M{"name": "Updated vendor"}, 400)
	rec := httptest.NewRecorder()
	handler(searchOptions)(rec, httptest.NewRequest("GET", "/?resource=vendors&q=Updated", nil))
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte("Updated vendor")) {
		t.Fatalf("vendor search: %d %s", rec.Code, rec.Body.String())
	}
	call(lookup("categories"), "POST", M{"name": "Category"}, 200)
	call(items, "POST", M{"sku": "TEST-1", "name": "Test item", "brand_id": 1, "category_id": 1, "unit": "pcs", "reorder_level": 2}, 200)
	call(requests, "POST", M{"item_id": 1, "quantity": 10, "estimated_cost": 100, "reason": "Initial stock"}, 200)
	call(orders, "POST", M{"request_id": 1, "supplier": "Supplier"}, 400) // no order before approval
	call(decision, "POST", M{"id": 1, "decision": "approved"}, 200)
	call(orders, "POST", M{"request_id": 1, "supplier": "Supplier"}, 200)
	call(receipts, "POST", M{"order_id": 1, "quantity": 6}, 200)
	call(receipts, "POST", M{"order_id": 1, "quantity": 5}, 400)
	call(receipts, "POST", M{"order_id": 1, "quantity": 4}, 200)
	call(requests, "POST", M{"item_id": 1, "quantity": 10, "estimated_cost": 120, "reason": "Replenishment"}, 200)
	call(decision, "POST", M{"id": 2, "decision": "approved"}, 200)
	call(orders, "POST", M{"request_id": 2, "supplier": "Supplier"}, 200)
	call(receipts, "POST", M{"order_id": 2, "quantity": 10}, 200)
	call(items, "PUT", M{"id": 1, "sku": "TEST-1", "name": "Updated item", "brand_id": 1, "category_id": 1, "unit": "pcs", "reorder_level": 2}, 200)
	call(sales, "POST", M{"item_id": 1, "quantity": 3, "unit_price": 150, "note": "Customer"}, 200)
	call(issues, "POST", M{"item_id": 1, "quantity": 30, "unit_price": 0, "note": "Test"}, 400)
	var qty, cost, cogs float64
	if err = db.QueryRow(`SELECT quantity,average_cost FROM items WHERE id=1`).Scan(&qty, &cost); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT quantity*unit_cost FROM sales WHERE id=1`).Scan(&cogs); err != nil {
		t.Fatal(err)
	}
	if qty != 17 || cost != 110 || cogs != 330 {
		t.Fatalf("unexpected stock/cost/COGS: %v %v %v", qty, cost, cogs)
	}
	call(recalculate, "POST", M{}, 200)
	call(monthly, "POST", M{}, 400) // current calendar month cannot be closed
	// Move only this disposable fixture to the previous month to exercise closing.
	_, err = db.Exec(`UPDATE movements SET created_at=DATE_SUB(CURRENT_TIMESTAMP,INTERVAL 1 MONTH); UPDATE sales SET created_at=DATE_SUB(CURRENT_TIMESTAMP,INTERVAL 1 MONTH); UPDATE accounting_periods SET period=DATE_FORMAT(DATE_SUB(CURRENT_DATE(),INTERVAL 1 MONTH),'%Y-%m') WHERE status='open'`)
	if err != nil {
		t.Fatal(err)
	}
	call(sales, "POST", M{"item_id": 1, "quantity": 1, "unit_price": 150, "note": "blocked"}, 400)
	call(monthly, "POST", M{}, 200)
	var closingQty, closingCost float64
	if err = db.QueryRow(`SELECT quantity,unit_cost FROM monthly_stock_snapshots WHERE item_id=1`).Scan(&closingQty, &closingCost); err != nil {
		t.Fatal(err)
	}
	if closingQty != 17 || closingCost != 110 {
		t.Fatalf("unexpected closing balance: %v %v", closingQty, closingCost)
	}
	// A historical source change must not be replayed into a closed period.
	if _, err = db.Exec(`UPDATE movements SET unit_cost=50 WHERE kind='GR'`); err != nil {
		t.Fatal(err)
	}
	call(sales, "POST", M{"item_id": 1, "quantity": 2, "unit_price": 150, "note": "Current month"}, 200)
	call(recalculate, "POST", M{}, 200)
	var oldCost, newCost float64
	if err = db.QueryRow(`SELECT unit_cost FROM sales WHERE id=1`).Scan(&oldCost); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT unit_cost FROM sales WHERE id=2`).Scan(&newCost); err != nil {
		t.Fatal(err)
	}
	if oldCost != 110 || newCost != 110 {
		t.Fatal("recalculation changed closed-period cost or current cost is incorrect")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM transaction_history WHERE entity_type='monthly_stock_snapshots' AND action='INSERT'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("monthly history missing: %v, %v", count, err)
	}
	// A multi-line document must post all lines together, or none on error.
	call(items, "POST", M{"sku": "TEST-2", "name": "Second item", "brand_id": 1, "category_id": 1, "unit": "pcs", "reorder_level": 0}, 200)
	call(documents, "POST", M{"kind": "PR", "reference_id": 0, "party": "", "note": "Restock", "lines": []M{{"item_id": 1, "quantity": 4, "unit_price": 100, "source_id": 0}, {"item_id": 2, "quantity": 3, "unit_price": 50, "source_id": 0}}}, 200)
	var pr int64
	if err = db.QueryRow(`SELECT id FROM document_headers WHERE kind='PR'`).Scan(&pr); err != nil {
		t.Fatal(err)
	}
	call(documentDecision, "POST", M{"id": pr, "decision": "approved"}, 200)
	var l1, l2 int64
	if err = db.QueryRow(`SELECT MIN(id),MAX(id) FROM document_lines WHERE document_id=?`, pr).Scan(&l1, &l2); err != nil {
		t.Fatal(err)
	}
	call(documents, "POST", M{"kind": "PO", "reference_id": pr, "party": "Supplier", "note": "", "lines": []M{{"item_id": 1, "quantity": 4, "unit_price": 100, "source_id": l1}, {"item_id": 2, "quantity": 3, "unit_price": 50, "source_id": l2}}}, 200)
	var po int64
	if err = db.QueryRow(`SELECT id FROM document_headers WHERE kind='PO'`).Scan(&po); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT MIN(id),MAX(id) FROM document_lines WHERE document_id=?`, po).Scan(&l1, &l2); err != nil {
		t.Fatal(err)
	}
	call(documents, "POST", M{"kind": "GR", "reference_id": po, "party": "", "note": "", "lines": []M{{"item_id": 1, "quantity": 4, "unit_price": 100, "source_id": l1}, {"item_id": 2, "quantity": 3, "unit_price": 50, "source_id": l2}}}, 200)
	var before float64
	if err = db.QueryRow(`SELECT quantity FROM items WHERE id=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	call(documents, "POST", M{"kind": "SALE", "reference_id": 0, "party": "Buyer", "note": "", "lines": []M{{"item_id": 1, "quantity": 1, "unit_price": 150, "source_id": 0}, {"item_id": 2, "quantity": 999, "unit_price": 90, "source_id": 0}}}, 400)
	var after float64
	if err = db.QueryRow(`SELECT quantity FROM items WHERE id=1`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("failed document changed stock")
	}
	call(documents, "POST", M{"kind": "SALE", "reference_id": 0, "party": "Buyer", "note": "", "lines": []M{{"item_id": 1, "quantity": 1, "unit_price": 150, "source_id": 0}, {"item_id": 2, "quantity": 2, "unit_price": 90, "source_id": 0}}}, 200)
	call(documents, "GET", nil, 200)
	call(topItems, "GET", nil, 200)
	call(recalculate, "POST", M{}, 200)
}
