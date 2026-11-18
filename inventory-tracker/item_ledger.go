package main

import (
	"database/sql"
	"net/http"
	"strings"
)

func recordStockEvent(tx *sql.Tx, result sql.Result, itemID int64, change, before float64) error {
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO item_stock_events(movement_id,item_id,action,quantity_change,stock_before,stock_after) VALUES(?,?,'POST',?,?,?)`, id, itemID, change, before, money(before+change))
	return err
}

func itemLedger(w http.ResponseWriter, r *http.Request) error {
	if err := method(r, "GET"); err != nil {
		return err
	}
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(term) > 100 {
		return bad("Search text is too long.")
	}
	pattern := "%" + term + "%"
	result, err := rows(`SELECT ledger.* FROM (
 SELECT e.id event_id,e.movement_id,e.item_id,i.sku,i.name item,i.unit,m.kind,e.action,e.quantity_change,e.stock_before,e.stock_after,m.unit_cost,m.note,m.voided,e.created_at,ABS(m.quantity) transaction_quantity,'' record_status,
 (SELECT h.id FROM document_lines l JOIN document_headers h ON h.id=l.document_id WHERE l.legacy_id=m.id AND h.kind=m.kind LIMIT 1) document_id
 FROM item_stock_events e JOIN movements m ON m.id=e.movement_id JOIN items i ON i.id=e.item_id
 UNION ALL
 SELECT 0 event_id,m.id movement_id,m.item_id,i.sku,i.name item,i.unit,m.kind,'LEGACY' action,m.quantity quantity_change,NULL stock_before,NULL stock_after,m.unit_cost,m.note,m.voided,m.created_at,ABS(m.quantity) transaction_quantity,'' record_status,
 (SELECT h.id FROM document_lines l JOIN document_headers h ON h.id=l.document_id WHERE l.legacy_id=m.id AND h.kind=m.kind LIMIT 1) document_id
 FROM movements m JOIN items i ON i.id=m.item_id WHERE NOT EXISTS (SELECT 1 FROM item_stock_events e WHERE e.movement_id=m.id AND e.action='POST')
 UNION ALL
 SELECT 0,p.id,p.item_id,i.sku,i.name,i.unit,'PR','DOCUMENT',0,NULL,NULL,p.estimated_cost,p.reason,0,p.created_at,p.quantity,p.status,
 (SELECT h.id FROM document_lines l JOIN document_headers h ON h.id=l.document_id WHERE l.legacy_id=p.id AND h.kind='PR' LIMIT 1)
 FROM purchase_requests p JOIN items i ON i.id=p.item_id
 UNION ALL
 SELECT 0,p.id,p.item_id,i.sku,i.name,i.unit,'PO','DOCUMENT',0,NULL,NULL,p.unit_cost,p.supplier,0,p.created_at,p.quantity,p.status,
 (SELECT h.id FROM document_lines l JOIN document_headers h ON h.id=l.document_id WHERE l.legacy_id=p.id AND h.kind='PO' LIMIT 1)
 FROM purchase_orders p JOIN items i ON i.id=p.item_id
 ) ledger WHERE (?='' OR sku LIKE ? OR item LIKE ? OR kind LIKE ? OR note LIKE ? OR CAST(movement_id AS CHAR) LIKE ? OR CAST(document_id AS CHAR) LIKE ?) ORDER BY created_at DESC,event_id DESC,movement_id DESC LIMIT 1000`, term, pattern, pattern, pattern, pattern, pattern, pattern)
	if err != nil {
		return err
	}
	respond(w, result)
	return nil
}
