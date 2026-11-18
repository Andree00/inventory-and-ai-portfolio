package main

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
)

func migrateTransactionActions() error {
	if _, e := db.Exec(`CREATE TABLE IF NOT EXISTS item_stock_events (id BIGINT AUTO_INCREMENT PRIMARY KEY, movement_id BIGINT NOT NULL, item_id BIGINT NOT NULL, action VARCHAR(12) NOT NULL, quantity_change DECIMAL(16,4) NOT NULL, stock_before DECIMAL(16,4) NOT NULL, stock_after DECIMAL(16,4) NOT NULL, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, UNIQUE KEY movement_action (movement_id,action), INDEX(item_id,id), FOREIGN KEY (movement_id) REFERENCES movements(id), FOREIGN KEY (item_id) REFERENCES items(id))`); e != nil {
		return e
	}
	for _, table := range []string{"movements", "sales"} {
		var n int
		if e := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name='voided'`, table).Scan(&n); e != nil {
			return e
		}
		if n == 0 {
			if _, e := db.Exec("ALTER TABLE " + table + " ADD COLUMN voided BOOLEAN NOT NULL DEFAULT 0"); e != nil {
				return e
			}
		}
	}
	return nil
}

type editLine struct {
	ID        int64   `json:"id"`
	ItemID    int64   `json:"item_id"`
	Quantity  float64 `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
}
type editDocumentInput struct {
	ID    int64      `json:"id"`
	Party string     `json:"party"`
	Note  string     `json:"note"`
	Lines []editLine `json:"lines"`
}

func openDocument(tx *sql.Tx, id int64, open string) (kind, status, period string, e error) {
	e = tx.QueryRow(`SELECT kind,status,DATE_FORMAT(created_at,'%Y-%m') FROM document_headers WHERE id=? FOR UPDATE`, id).Scan(&kind, &status, &period)
	if errors.Is(e, sql.ErrNoRows) {
		e = bad("Document not found.")
	}
	if e == nil && period != open {
		e = bad("Transactions from a closed period cannot be changed.")
	}
	return
}
func editDocument(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b editDocumentInput
	if e := decode(r, &b); e != nil {
		return e
	}
	b.Note = strings.TrimSpace(b.Note)
	b.Party = strings.TrimSpace(b.Party)
	if b.ID < 1 || len(b.Lines) < 1 || len(b.Lines) > 50 || len(b.Note) > 255 || len(b.Party) > 150 {
		return bad("Enter a valid header and detail lines.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	open, e := lockPeriod(tx, true)
	if e != nil {
		return e
	}
	kind, status, _, e := openDocument(tx, b.ID, open)
	if e != nil {
		return e
	}
	if kind == "PR" {
		if status != "pending" && status != "unposted" {
			return bad("Only pending or unposted purchase requests can be edited.")
		}
		if b.Note == "" {
			return bad("A request reason is required.")
		}
	} else if kind == "PO" {
		if status != "open" && status != "unposted" {
			return bad("Only open or unposted purchase orders can be edited.")
		}
		if b.Party == "" {
			return bad("A supplier is required.")
		}
	} else {
		return bad("Only open purchase requests and orders can be edited.")
	}
	rows, e := tx.Query(`SELECT id,legacy_id,item_id FROM document_lines WHERE document_id=? ORDER BY id FOR UPDATE`, b.ID)
	if e != nil {
		return e
	}
	type oldLine struct{ id, legacy, item int64 }
	old := map[int64]oldLine{}
	for rows.Next() {
		var x oldLine
		if e = rows.Scan(&x.id, &x.legacy, &x.item); e != nil {
			rows.Close()
			return e
		}
		old[x.id] = x
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(old) != len(b.Lines) {
		return bad("Edit the existing lines; adding or removing lines is not supported here.")
	}
	seen := map[int64]bool{}
	for _, line := range b.Lines {
		previous, ok := old[line.ID]
		if !ok || seen[line.ID] || line.ItemID < 1 || !validPositive(line.Quantity) || !validPositive(line.UnitPrice) {
			return bad("Check the item, quantity and unit cost on every line.")
		}
		seen[line.ID] = true
		if kind == "PR" {
			var count int
			if e = tx.QueryRow("SELECT COUNT(*) FROM purchase_orders WHERE request_id=?", previous.legacy).Scan(&count); e != nil {
				return e
			}
			if count > 0 {
				var existingItem int64
				var existingQty float64
				if e = tx.QueryRow("SELECT item_id,quantity FROM purchase_requests WHERE id=?", previous.legacy).Scan(&existingItem, &existingQty); e != nil {
					return e
				}
				if existingItem != line.ItemID || money(existingQty) != money(line.Quantity) {
					return bad("An ordered request must keep its original item and quantity.")
				}
			}
			if _, e = tx.Exec("UPDATE purchase_requests SET item_id=?,quantity=?,estimated_cost=?,reason=?,status=? WHERE id=?", line.ItemID, line.Quantity, line.UnitPrice, b.Note, status, previous.legacy); e != nil {
				return bad("Could not update the request item.")
			}
		} else {
			var received, requested float64
			var state, requestStatus string
			if e = tx.QueryRow(`SELECT o.received,o.status,p.quantity,p.status FROM purchase_orders o JOIN purchase_requests p ON p.id=o.request_id WHERE o.id=? FOR UPDATE`, previous.legacy).Scan(&received, &state, &requested, &requestStatus); e != nil {
				return e
			}
			if received != 0 || requestStatus != "approved" {
				return bad("An order with receipts cannot be edited.")
			}
			if line.ItemID != previous.item || money(line.Quantity) != money(requested) {
				return bad("Order items and quantities must match their approved request.")
			}
			if _, e = tx.Exec("UPDATE purchase_orders SET quantity=?,unit_cost=?,supplier=?,status=? WHERE id=?", line.Quantity, line.UnitPrice, b.Party, status, previous.legacy); e != nil {
				return e
			}
		}
		if _, e = tx.Exec("UPDATE document_lines SET item_id=?,quantity=?,unit_price=? WHERE id=? AND document_id=?", line.ItemID, line.Quantity, line.UnitPrice, line.ID, b.ID); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("UPDATE document_headers SET party=?,note=? WHERE id=?", b.Party, b.Note, b.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true})
	return nil
}
func editLegacyTransaction(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		Kind     string  `json:"kind"`
		ID       int64   `json:"id"`
		ItemID   int64   `json:"item_id"`
		Quantity float64 `json:"quantity"`
		UnitCost float64 `json:"unit_cost"`
		Note     string  `json:"note"`
		Party    string  `json:"party"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	b.Note = strings.TrimSpace(b.Note)
	b.Party = strings.TrimSpace(b.Party)
	if b.ID < 1 || !validPositive(b.Quantity) || !validPositive(b.UnitCost) {
		return bad("Enter a valid quantity and unit cost.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	open, e := lockPeriod(tx, true)
	if e != nil {
		return e
	}
	var attached int
	if b.Kind == "PR" {
		var status, month string
		if e = tx.QueryRow(`SELECT status,DATE_FORMAT(created_at,'%Y-%m') FROM purchase_requests WHERE id=? FOR UPDATE`, b.ID).Scan(&status, &month); e != nil {
			return bad("Request not found.")
		}
		if month != open || !(status == "pending" || status == "unposted") {
			return bad("Only current-month pending or unposted requests can be edited.")
		}
		if b.ItemID < 1 || b.Note == "" || len(b.Note) > 255 {
			return bad("Choose an item and enter a reason.")
		}
		if e = tx.QueryRow(`SELECT COUNT(*) FROM document_lines l JOIN document_headers h ON h.id=l.document_id WHERE h.kind='PR' AND l.legacy_id=?`, b.ID).Scan(&attached); e != nil {
			return e
		}
		if attached > 0 {
			return bad("Edit the parent document instead.")
		}
		if e = tx.QueryRow("SELECT COUNT(*) FROM purchase_orders WHERE request_id=?", b.ID).Scan(&attached); e != nil {
			return e
		}
		if attached > 0 {
			var existingItem int64
			var existingQty float64
			if e = tx.QueryRow("SELECT item_id,quantity FROM purchase_requests WHERE id=?", b.ID).Scan(&existingItem, &existingQty); e != nil {
				return e
			}
			if existingItem != b.ItemID || money(existingQty) != money(b.Quantity) {
				return bad("An ordered request must keep its original item and quantity.")
			}
		}
		if _, e = tx.Exec(`UPDATE purchase_requests SET item_id=?,quantity=?,estimated_cost=?,reason=? WHERE id=?`, b.ItemID, b.Quantity, b.UnitCost, b.Note, b.ID); e != nil {
			return bad("Could not update the request.")
		}
	} else if b.Kind == "PO" {
		var status, month, requestStatus string
		var received, requested float64
		if e = tx.QueryRow(`SELECT o.status,DATE_FORMAT(o.created_at,'%Y-%m'),o.received,p.quantity,p.status FROM purchase_orders o JOIN purchase_requests p ON p.id=o.request_id WHERE o.id=? FOR UPDATE`, b.ID).Scan(&status, &month, &received, &requested, &requestStatus); e != nil {
			return bad("Order not found.")
		}
		if month != open || received != 0 || requestStatus != "approved" || money(b.Quantity) != money(requested) || (status != "open" && status != "unposted") {
			return bad("Only current-month orders without receipts can be edited.")
		}
		if b.Party == "" || len(b.Party) > 150 {
			return bad("Enter a supplier.")
		}
		if e = tx.QueryRow(`SELECT COUNT(*) FROM document_lines l JOIN document_headers h ON h.id=l.document_id WHERE h.kind='PO' AND l.legacy_id=?`, b.ID).Scan(&attached); e != nil {
			return e
		}
		if attached > 0 {
			return bad("Edit the parent document instead.")
		}
		if _, e = tx.Exec(`UPDATE purchase_orders SET quantity=?,unit_cost=?,supplier=? WHERE id=?`, b.Quantity, b.UnitCost, b.Party, b.ID); e != nil {
			return e
		}
	} else {
		return bad("Unsupported transaction type.")
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true})
	return nil
}

type voidMovement struct {
	id, itemID        int64
	kind, note, month string
	quantity          float64
	saleID            sql.NullInt64
	sourceID          sql.NullInt64
}

// Posting an unposted request/order is explicit; saving an edit does not post it.
func postDocument(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		ID int64 `json:"id"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if b.ID < 1 {
		return bad("A valid document is required.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	open, e := lockPeriod(tx, true)
	if e != nil {
		return e
	}
	kind, status, _, e := openDocument(tx, b.ID, open)
	if e != nil {
		return e
	}
	if status != "unposted" {
		return bad("Only an open, unposted document can be posted.")
	}
	var total, invalid int
	if kind == "PR" {
		if e = tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN p.id IS NULL OR p.status<>'unposted' OR p.quantity<=0 OR p.estimated_cost<=0 OR TRIM(p.reason)='' THEN 1 ELSE 0 END),0) FROM document_lines l LEFT JOIN purchase_requests p ON p.id=l.legacy_id WHERE l.document_id=?`, b.ID).Scan(&total, &invalid); e != nil {
			return e
		}
		if total == 0 || invalid > 0 {
			return bad("The request lines are not ready to post.")
		}
		if _, e = tx.Exec(`UPDATE purchase_requests p JOIN document_lines l ON l.legacy_id=p.id SET p.status='pending' WHERE l.document_id=?`, b.ID); e != nil {
			return e
		}
		if _, e = tx.Exec(`UPDATE document_headers SET status='pending' WHERE id=?`, b.ID); e != nil {
			return e
		}
	} else if kind == "PO" {
		var reference sql.NullInt64
		if e = tx.QueryRow("SELECT reference_id FROM document_headers WHERE id=?", b.ID).Scan(&reference); e != nil {
			return e
		}
		if !reference.Valid {
			return bad("The approved source request is missing.")
		}
		var sourceKind, sourceStatus string
		if e = tx.QueryRow("SELECT kind,status FROM document_headers WHERE id=? FOR UPDATE", reference.Int64).Scan(&sourceKind, &sourceStatus); e != nil || sourceKind != "PR" || sourceStatus != "approved" {
			return bad("The source request must be approved before posting this order.")
		}
		if e = tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN o.id IS NULL OR p.id IS NULL OR o.status<>'unposted' OR o.received<>0 OR o.quantity<=0 OR o.unit_cost<=0 OR TRIM(o.supplier)='' OR p.status<>'approved' OR o.item_id<>p.item_id OR o.quantity<>p.quantity THEN 1 ELSE 0 END),0) FROM document_lines l LEFT JOIN purchase_orders o ON o.id=l.legacy_id LEFT JOIN purchase_requests p ON p.id=o.request_id WHERE l.document_id=?`, b.ID).Scan(&total, &invalid); e != nil {
			return e
		}
		if total == 0 || invalid > 0 {
			return bad("Order lines must match an approved request and have no receipts.")
		}
		if _, e = tx.Exec(`UPDATE purchase_orders p JOIN document_lines l ON l.legacy_id=p.id SET p.status='open' WHERE l.document_id=?`, b.ID); e != nil {
			return e
		}
		if _, e = tx.Exec(`UPDATE document_headers SET status='open' WHERE id=?`, b.ID); e != nil {
			return e
		}
	} else {
		return bad("Unposted stock transactions must be replaced by a new document.")
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true})
	return nil
}

func postLegacyTransaction(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		Kind string `json:"kind"`
		ID   int64  `json:"id"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if b.ID < 1 || (b.Kind != "PR" && b.Kind != "PO") {
		return bad("Select an unposted request or order.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	open, e := lockPeriod(tx, true)
	if e != nil {
		return e
	}
	table := "purchase_requests"
	if b.Kind == "PO" {
		table = "purchase_orders"
	}
	var state, month string
	if e = tx.QueryRow("SELECT status,DATE_FORMAT(created_at,'%Y-%m') FROM "+table+" WHERE id=? FOR UPDATE", b.ID).Scan(&state, &month); e != nil {
		return bad("Transaction not found.")
	}
	if month != open {
		return bad("Transactions from a closed period cannot be posted.")
	}
	if state != "unposted" {
		return bad("Only an open, unposted transaction can be posted.")
	}
	var attached int
	if e = tx.QueryRow(`SELECT COUNT(*) FROM document_lines l JOIN document_headers h ON h.id=l.document_id WHERE l.legacy_id=? AND h.kind=?`, b.ID, b.Kind).Scan(&attached); e != nil {
		return e
	}
	if attached > 0 {
		return bad("Post the parent document instead.")
	}
	if b.Kind == "PR" {
		var valid int
		if e = tx.QueryRow(`SELECT COUNT(*) FROM purchase_requests WHERE id=? AND quantity>0 AND estimated_cost>0 AND TRIM(reason)<>''`, b.ID).Scan(&valid); e != nil {
			return e
		}
		if valid != 1 {
			return bad("The request is incomplete.")
		}
		if _, e = tx.Exec("UPDATE purchase_requests SET status='pending' WHERE id=?", b.ID); e != nil {
			return e
		}
	} else {
		var valid int
		if e = tx.QueryRow(`SELECT COUNT(*) FROM purchase_orders o JOIN purchase_requests p ON p.id=o.request_id WHERE o.id=? AND o.received=0 AND o.quantity>0 AND o.unit_cost>0 AND TRIM(o.supplier)<>'' AND p.status='approved' AND o.item_id=p.item_id AND o.quantity=p.quantity`, b.ID).Scan(&valid); e != nil {
			return e
		}
		if valid != 1 {
			return bad("The order must match an approved request and have no receipts.")
		}
		if _, e = tx.Exec("UPDATE purchase_orders SET status='open' WHERE id=?", b.ID); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true})
	return nil
}

func unpostDocument(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		ID int64 `json:"id"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if b.ID < 1 {
		return bad("A valid document is required.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	open, e := lockPeriod(tx, true)
	if e != nil {
		return e
	}
	kind, status, _, e := openDocument(tx, b.ID, open)
	if e != nil {
		return e
	}
	if status == "unposted" {
		return bad("This document is already unposted.")
	}
	switch kind {
	case "PR":
		if status != "pending" && status != "approved" {
			return bad("Only pending or approved requests can be unposted.")
		}
		var dependencies int
		if e = tx.QueryRow(`SELECT COUNT(*) FROM document_lines l JOIN purchase_orders p ON p.request_id=l.legacy_id WHERE l.document_id=? AND p.status<>'unposted'`, b.ID).Scan(&dependencies); e != nil {
			return e
		}
		if dependencies > 0 {
			return bad("This request has purchase orders; unpost or resolve its dependent orders first.")
		}
		if _, e = tx.Exec(`UPDATE purchase_requests p JOIN document_lines l ON l.legacy_id=p.id SET p.status='unposted' WHERE l.document_id=?`, b.ID); e != nil {
			return e
		}
	case "PO":
		if status != "open" {
			return bad("Only open orders without receipts can be unposted.")
		}
		var received int
		if e = tx.QueryRow(`SELECT COUNT(*) FROM document_lines l JOIN purchase_orders p ON p.id=l.legacy_id WHERE l.document_id=? AND p.received<>0`, b.ID).Scan(&received); e != nil {
			return e
		}
		if received > 0 {
			return bad("Unpost all receipts for this order first.")
		}
		if _, e = tx.Exec(`UPDATE purchase_orders p JOIN document_lines l ON l.legacy_id=p.id SET p.status='unposted' WHERE l.document_id=?`, b.ID); e != nil {
			return e
		}
	case "GR", "GI", "SALE":
		if status != "posted" {
			return bad("Only posted stock transactions can be unposted.")
		}
		rs, e := tx.Query(`SELECT m.id,m.item_id,m.kind,m.note,DATE_FORMAT(m.created_at,'%Y-%m'),m.quantity,m.sale_id,l.source_id FROM document_lines l JOIN movements m ON m.id=l.legacy_id WHERE l.document_id=? ORDER BY m.id DESC FOR UPDATE`, b.ID)
		if e != nil {
			return e
		}
		var moves []voidMovement
		for rs.Next() {
			var m voidMovement
			if e = rs.Scan(&m.id, &m.itemID, &m.kind, &m.note, &m.month, &m.quantity, &m.saleID, &m.sourceID); e != nil {
				rs.Close()
				return e
			}
			moves = append(moves, m)
		}
		e = rs.Err()
		rs.Close()
		if e != nil {
			return e
		}
		if len(moves) == 0 {
			return bad("Document has no stock movements.")
		}
		for _, m := range moves {
			if m.kind != kind {
				return bad("Document and stock movement types do not match.")
			}
		}
		if e = voidMovements(tx, open, moves, b.ID); e != nil {
			return e
		}
	default:
		return bad("This document type cannot be unposted.")
	}
	if _, e = tx.Exec("UPDATE document_headers SET status='unposted' WHERE id=?", b.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true})
	return nil
}
func unpostLegacyTransaction(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		Kind string `json:"kind"`
		ID   int64  `json:"id"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if b.ID < 1 {
		return bad("A valid transaction is required.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	open, e := lockPeriod(tx, true)
	if e != nil {
		return e
	}
	if b.Kind == "PR" || b.Kind == "PO" {
		table := "purchase_requests"
		if b.Kind == "PO" {
			table = "purchase_orders"
		}
		var state, month string
		if e = tx.QueryRow("SELECT status,DATE_FORMAT(created_at,'%Y-%m') FROM "+table+" WHERE id=? FOR UPDATE", b.ID).Scan(&state, &month); e != nil {
			return bad("Transaction not found.")
		}
		if month != open {
			return bad("Transactions from a closed period cannot be changed.")
		}
		if state == "unposted" {
			return bad("This transaction is already unposted.")
		}
		var n int
		if e = tx.QueryRow(`SELECT COUNT(*) FROM document_lines l JOIN document_headers h ON h.id=l.document_id WHERE l.legacy_id=? AND h.kind=?`, b.ID, b.Kind).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return bad("Unpost the parent document instead.")
		}
		if b.Kind == "PR" {
			if state != "pending" && state != "approved" {
				return bad("Only pending or approved requests can be unposted.")
			}
			if e = tx.QueryRow("SELECT COUNT(*) FROM purchase_orders WHERE request_id=? AND status<>'unposted'", b.ID).Scan(&n); e != nil {
				return e
			}
			if n > 0 {
				return bad("A purchase order already references this request.")
			}
		} else {
			var received float64
			if e = tx.QueryRow("SELECT received FROM purchase_orders WHERE id=?", b.ID).Scan(&received); e != nil {
				return e
			}
			if state != "open" || received != 0 {
				return bad("Only orders without receipts can be unposted.")
			}
		}
		if _, e = tx.Exec("UPDATE "+table+" SET status='unposted' WHERE id=?", b.ID); e != nil {
			return e
		}
	} else if b.Kind == "MOVEMENT" {
		var m voidMovement
		var voided bool
		if e = tx.QueryRow(`SELECT id,item_id,kind,note,DATE_FORMAT(created_at,'%Y-%m'),quantity,sale_id,voided FROM movements WHERE id=? FOR UPDATE`, b.ID).Scan(&m.id, &m.itemID, &m.kind, &m.note, &m.month, &m.quantity, &m.saleID, &voided); e != nil {
			return bad("Movement not found.")
		}
		if voided {
			return bad("This movement is already unposted.")
		}
		var n int
		if e = tx.QueryRow(`SELECT COUNT(*) FROM document_lines l JOIN document_headers h ON h.id=l.document_id WHERE l.legacy_id=? AND h.kind IN ('GR','GI','SALE')`, m.id).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return bad("Unpost the parent document instead.")
		}
		if e = voidMovements(tx, open, []voidMovement{m}, 0); e != nil {
			return e
		}
	} else {
		return bad("Unsupported transaction type.")
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true})
	return nil
}

func voidMovements(tx *sql.Tx, open string, moves []voidMovement, documentID int64) error {
	allowed := map[int64]bool{}
	deltas := map[int64]float64{}
	oldStock := map[int64]float64{}
	for _, m := range moves {
		if m.month != open {
			return bad("Transactions from a closed period cannot be unposted.")
		}
		if m.kind != "GR" && m.kind != "GI" && m.kind != "SALE" {
			return bad("Unsupported stock movement.")
		}
		if (m.kind == "GR" && m.quantity <= 0) || (m.kind != "GR" && m.quantity >= 0) || (m.kind != "SALE" && m.saleID.Valid) {
			return bad("The stock movement is inconsistent and cannot be unposted safely.")
		}
		allowed[m.id] = true
		deltas[m.itemID] += m.quantity
	}
	for item := range deltas {
		var qty float64
		if e := tx.QueryRow("SELECT quantity FROM items WHERE id=? FOR UPDATE", item).Scan(&qty); e != nil {
			return e
		}
		oldStock[item] = qty
	}
	for _, m := range moves {
		var already bool
		if e := tx.QueryRow("SELECT voided FROM movements WHERE id=? FOR UPDATE", m.id).Scan(&already); e != nil {
			return e
		}
		if already {
			return bad("A movement is already unposted.")
		}
		rs, e := tx.Query("SELECT id FROM movements WHERE item_id=? AND id>? AND voided=0 FOR UPDATE", m.itemID, m.id)
		if e != nil {
			return e
		}
		for rs.Next() {
			var id int64
			if e = rs.Scan(&id); e != nil {
				rs.Close()
				return e
			}
			if !allowed[id] {
				rs.Close()
				return bad("Unpost later stock movements for this item first.")
			}
		}
		e = rs.Err()
		rs.Close()
		if e != nil {
			return e
		}
	}
	for _, m := range moves {
		if _, e := tx.Exec(`INSERT INTO item_stock_events(movement_id,item_id,action,quantity_change,stock_before,stock_after) VALUES(?,?,'UNPOST',?,?,?)`, m.id, m.itemID, -m.quantity, oldStock[m.itemID], money(oldStock[m.itemID]-m.quantity)); e != nil {
			return e
		}
		oldStock[m.itemID] = money(oldStock[m.itemID] - m.quantity)
		if m.kind == "SALE" && !m.saleID.Valid {
			return bad("This sale has no linked sales record and cannot be unposted safely.")
		}
		if m.kind == "GR" {
			var orderID int64
			if m.sourceID.Valid {
				if e := tx.QueryRow(`SELECT legacy_id FROM document_lines WHERE id=?`, m.sourceID.Int64).Scan(&orderID); e != nil {
					return bad("The source order line is missing.")
				}
			} else {
				if n, e := fmt.Sscanf(m.note, "Purchase order #%d", &orderID); n != 1 || e != nil || orderID < 1 {
					return bad("The original purchase order cannot be identified safely.")
				}
			}
			res, e := tx.Exec("UPDATE purchase_orders SET received=received-?,status='open' WHERE id=? AND item_id=? AND received>=?", m.quantity, orderID, m.itemID, m.quantity)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return bad("The order receipt balance cannot be reconciled.")
			}
		}
		if _, e := tx.Exec("UPDATE movements SET voided=1 WHERE id=?", m.id); e != nil {
			return e
		}
		if _, e := tx.Exec(`INSERT INTO transaction_history(entity_type,entity_id,action,before_data,after_data) VALUES('movements',?,'UNPOST',JSON_OBJECT('voided',0),JSON_OBJECT('voided',1))`, m.id); e != nil {
			return e
		}
		if m.saleID.Valid {
			res, e := tx.Exec("UPDATE sales SET voided=1 WHERE id=? AND item_id=? AND voided=0", m.saleID.Int64, m.itemID)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return bad("The linked sale cannot be reconciled.")
			}
			if _, e = tx.Exec(`INSERT INTO transaction_history(entity_type,entity_id,action,before_data,after_data) VALUES('sales',?,'UNPOST',JSON_OBJECT('voided',0),JSON_OBJECT('voided',1))`, m.saleID.Int64); e != nil {
				return e
			}
		}
	}
	for item := range deltas {
		qty, cost, e := replayBalance(tx, item, open)
		if e != nil {
			return e
		}
		if math.Abs(qty-oldStock[item]) > 0.0001 {
			return bad("The stock ledger does not reconcile; nothing was unposted.")
		}
		if _, e = tx.Exec("UPDATE items SET quantity=?,average_cost=? WHERE id=?", qty, cost, item); e != nil {
			return e
		}
	}
	if documentID > 0 && len(moves) > 0 && moves[0].kind == "GR" {
		if _, e := tx.Exec(`UPDATE document_headers p JOIN document_headers g ON g.reference_id=p.id SET p.status='open' WHERE g.id=? AND p.kind='PO'`, documentID); e != nil {
			return e
		}
	}
	return nil
}
func replayBalance(tx *sql.Tx, itemID int64, open string) (float64, float64, error) {
	qty, cost := 0.0, 0.0
	e := tx.QueryRow(`SELECT s.quantity,s.unit_cost FROM monthly_stock_snapshots s JOIN accounting_periods p ON p.period=s.period AND p.status='closed' WHERE s.item_id=? AND s.period<? ORDER BY s.period DESC LIMIT 1`, itemID, open).Scan(&qty, &cost)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return 0, 0, e
	}
	rs, e := tx.Query(`SELECT kind,quantity,unit_cost FROM movements WHERE item_id=? AND voided=0 AND created_at>=STR_TO_DATE(CONCAT(?,'-01'),'%Y-%m-%d') AND created_at<DATE_ADD(STR_TO_DATE(CONCAT(?,'-01'),'%Y-%m-%d'),INTERVAL 1 MONTH) ORDER BY id FOR UPDATE`, itemID, open, open)
	if e != nil {
		return 0, 0, e
	}
	defer rs.Close()
	for rs.Next() {
		var kind string
		var q, c float64
		if e = rs.Scan(&kind, &q, &c); e != nil {
			return 0, 0, e
		}
		switch kind {
		case "GR":
			if q <= 0 || c <= 0 {
				return 0, 0, bad("Invalid receipt in stock history.")
			}
			cost = money((qty*cost + q*c) / (qty + q))
			qty = money(qty + q)
		case "GI", "SALE":
			if q >= 0 || qty+q < -0.00001 {
				return 0, 0, bad("Stock history would become negative.")
			}
			qty = money(qty + q)
		default:
			return 0, 0, bad("Unknown stock movement type.")
		}
	}
	if e = rs.Err(); e != nil {
		return 0, 0, e
	}
	return qty, cost, nil
}
