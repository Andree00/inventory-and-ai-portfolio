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
	if err = migrateTransactionActions(); err != nil {
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
	var eventBefore, eventAfter, eventChange float64
	if err = db.QueryRow(`SELECT stock_before,stock_after,quantity_change FROM item_stock_events WHERE movement_id=1 AND action='POST'`).Scan(&eventBefore, &eventAfter, &eventChange); err != nil || eventBefore != 0 || eventAfter != 6 || eventChange != 6 {
		t.Fatalf("receipt event: %v %v %v %v", eventBefore, eventAfter, eventChange, err)
	}
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
	var gr, saleDoc int64
	if err = db.QueryRow(`SELECT id FROM document_headers WHERE kind='GR'`).Scan(&gr); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT id FROM document_headers WHERE kind='SALE'`).Scan(&saleDoc); err != nil {
		t.Fatal(err)
	}
	call(unpostDocument, "POST", M{"id": gr}, 400)                                                                                                                                                                     // later sale must be unposted first
	call(editDocument, "POST", M{"id": po, "party": "Supplier", "note": "", "lines": []M{{"id": l1, "item_id": 1, "quantity": 4, "unit_price": 100}, {"id": l2, "item_id": 2, "quantity": 3, "unit_price": 50}}}, 400) // receipt exists
	call(unpostDocument, "POST", M{"id": saleDoc}, 200)
	call(unpostDocument, "POST", M{"id": saleDoc}, 400)
	revenueResponse := httptest.NewRecorder()
	handler(overview)(revenueResponse, httptest.NewRequest("GET", "/", nil))
	var overviewData map[string]any
	if err = json.Unmarshal(revenueResponse.Body.Bytes(), &overviewData); err != nil {
		t.Fatal(err)
	}
	if overviewData["revenue"] != "750.00000000" {
		t.Fatalf("unposted sale still affects revenue: %v", overviewData["revenue"])
	}
	if err = db.QueryRow(`SELECT quantity FROM items WHERE id=2`).Scan(&after); err != nil || after != 3 {
		t.Fatalf("unposting sale did not restore stock: %v %v", after, err)
	}
	call(unpostDocument, "POST", M{"id": gr}, 200)
	if err = db.QueryRow(`SELECT quantity FROM items WHERE id=2`).Scan(&after); err != nil || after != 0 {
		t.Fatalf("unposting receipt did not reverse stock: %v %v", after, err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM document_headers WHERE id=?`, po).Scan(&status); err != nil || status != "open" {
		t.Fatalf("order did not reopen: %v %v", status, err)
	}
	call(unpostDocument, "POST", M{"id": po}, 200)
	call(editDocument, "POST", M{"id": po, "party": "New supplier", "note": "Updated", "lines": []M{{"id": l1, "item_id": 1, "quantity": 4, "unit_price": 105}, {"id": l2, "item_id": 2, "quantity": 3, "unit_price": 55}}}, 200)
	if err = db.QueryRow(`SELECT status FROM document_headers WHERE id=?`, po).Scan(&status); err != nil || status != "unposted" {
		t.Fatalf("editing should not post an order: %v %v", status, err)
	}
	call(postDocument, "POST", M{"id": po}, 200)
	call(postDocument, "POST", M{"id": po}, 400)
	if err = db.QueryRow(`SELECT COUNT(*) FROM transaction_history WHERE action='UNPOST' AND entity_type='movements'`).Scan(&count); err != nil || count < 4 {
		t.Fatalf("unpost history missing: %v %v", count, err)
	}
	call(unpostLegacyTransaction, "POST", M{"kind": "MOVEMENT", "id": 1}, 400) // closed-period movement
	call(editLegacyTransaction, "POST", M{"kind": "PR", "id": 1, "item_id": 1, "quantity": 10, "unit_cost": 100, "note": "Past", "party": ""}, 400)
	call(requests, "POST", M{"item_id": 2, "quantity": 5, "estimated_cost": 50, "reason": "Legacy request"}, 200)
	var legacyRequest int64
	if err = db.QueryRow(`SELECT MAX(id) FROM purchase_requests`).Scan(&legacyRequest); err != nil {
		t.Fatal(err)
	}
	legacyEdit := M{"kind": "PR", "id": legacyRequest, "item_id": 2, "quantity": 6, "unit_cost": 55, "note": "Updated request", "party": ""}
	call(editLegacyTransaction, "POST", legacyEdit, 200)
	call(unpostLegacyTransaction, "POST", M{"kind": "PR", "id": legacyRequest}, 200)
	call(editLegacyTransaction, "POST", legacyEdit, 200)
	call(postLegacyTransaction, "POST", M{"kind": "PR", "id": legacyRequest}, 200)
	call(decision, "POST", M{"id": legacyRequest, "decision": "approved"}, 200)
	call(orders, "POST", M{"request_id": legacyRequest, "supplier": "Supplier"}, 200)
	var legacyOrder int64
	if err = db.QueryRow(`SELECT MAX(id) FROM purchase_orders`).Scan(&legacyOrder); err != nil {
		t.Fatal(err)
	}
	orderEdit := M{"kind": "PO", "id": legacyOrder, "item_id": 2, "quantity": 6, "unit_cost": 60, "note": "", "party": "Supplier"}
	call(editLegacyTransaction, "POST", orderEdit, 200)
	call(unpostLegacyTransaction, "POST", M{"kind": "PO", "id": legacyOrder}, 200)
	call(editLegacyTransaction, "POST", orderEdit, 200)
	call(postLegacyTransaction, "POST", M{"kind": "PO", "id": legacyOrder}, 200)
	call(receipts, "POST", M{"order_id": legacyOrder, "quantity": 2}, 200)
	var receiptID int64
	if err = db.QueryRow(`SELECT MAX(id) FROM movements`).Scan(&receiptID); err != nil {
		t.Fatal(err)
	}
	call(issues, "POST", M{"item_id": 2, "quantity": 1, "unit_price": 0, "note": "Office"}, 200)
	var issueID int64
	if err = db.QueryRow(`SELECT MAX(id) FROM movements`).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	call(unpostLegacyTransaction, "POST", M{"kind": "MOVEMENT", "id": receiptID}, 400)
	call(unpostLegacyTransaction, "POST", M{"kind": "MOVEMENT", "id": issueID}, 200)
	if err = db.QueryRow(`SELECT stock_before,stock_after,quantity_change FROM item_stock_events WHERE movement_id=? AND action='UNPOST'`, issueID).Scan(&eventBefore, &eventAfter, &eventChange); err != nil || eventChange != 1 || eventAfter != eventBefore+1 {
		t.Fatalf("issue reversal event: %v %v %v %v", eventBefore, eventAfter, eventChange, err)
	}
	call(itemLedger, "GET", nil, 200)
	call(unpostLegacyTransaction, "POST", M{"kind": "MOVEMENT", "id": receiptID}, 200)
	if err = db.QueryRow(`SELECT quantity FROM items WHERE id=2`).Scan(&after); err != nil || after != 0 {
		t.Fatalf("legacy unpost stock: %v %v", after, err)
	}
	call(documents, "GET", nil, 200)
	call(topItems, "GET", nil, 200)
	call(getInsights, "GET", nil, 200)
	mockModel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("unexpected Ollama path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"Inventory summary."}`))
	}))
	defer mockModel.Close()
	t.Setenv("OLLAMA_URL", mockModel.URL)
	call(explainInsights, "POST", M{"kind": "daily"}, 200)
	call(explainInsights, "POST", M{"kind": "restock"}, 200)
	call(explainInsights, "POST", M{"kind": "other"}, 400)
	call(recalculate, "POST", M{}, 200)
}
