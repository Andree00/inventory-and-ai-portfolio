package main

import (
	"fmt"
	"net/http"
	"strings"
)

// The document layer groups the existing ledger records without replacing them.
func migrateDocuments() error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS document_headers(id BIGINT AUTO_INCREMENT PRIMARY KEY,kind VARCHAR(12) NOT NULL,status VARCHAR(16) NOT NULL,reference_id BIGINT NULL,party VARCHAR(150) NOT NULL DEFAULT '',note VARCHAR(255) NOT NULL DEFAULT '',created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(reference_id) REFERENCES document_headers(id));
CREATE TABLE IF NOT EXISTS document_lines(id BIGINT AUTO_INCREMENT PRIMARY KEY,document_id BIGINT NOT NULL,item_id BIGINT NOT NULL,quantity DECIMAL(16,4) NOT NULL,unit_price DECIMAL(16,4) NOT NULL,source_id BIGINT NULL,legacy_id BIGINT NOT NULL,FOREIGN KEY(document_id) REFERENCES document_headers(id),FOREIGN KEY(item_id) REFERENCES items(id));`)
	if e != nil {
		return e
	}
	defs := []struct{ table, fields string }{
		{"document_headers", "'kind',%s.kind,'status',%s.status,'reference_id',%s.reference_id,'party',%s.party,'note',%s.note"},
		{"document_lines", "'document_id',%s.document_id,'item_id',%s.item_id,'quantity',%s.quantity,'unit_price',%s.unit_price,'source_id',%s.source_id,'legacy_id',%s.legacy_id"},
	}
	for _, d := range defs {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			before, after, id := "NULL", "JSON_OBJECT("+strings.ReplaceAll(d.fields, "%s", "NEW")+")", "NEW.id"
			if op != "INSERT" {
				before = "JSON_OBJECT(" + strings.ReplaceAll(d.fields, "%s", "OLD") + ")"
			}
			if op == "DELETE" {
				after = "NULL"
				id = "OLD.id"
			}
			q := fmt.Sprintf("CREATE TRIGGER IF NOT EXISTS history_%s_%s AFTER %s ON %s FOR EACH ROW INSERT INTO transaction_history(entity_type,entity_id,action,before_data,after_data) VALUES('%s',%s,'%s',%s,%s)", d.table, strings.ToLower(op), op, d.table, d.table, id, op, before, after)
			if _, e = db.Exec(q); e != nil {
				return e
			}
		}
	}
	return nil
}

type documentLine struct {
	ItemID    int64   `json:"item_id"`
	Quantity  float64 `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
	SourceID  int64   `json:"source_id"`
}
type documentInput struct {
	Kind        string         `json:"kind"`
	ReferenceID int64          `json:"reference_id"`
	Party       string         `json:"party"`
	Note        string         `json:"note"`
	Lines       []documentLine `json:"lines"`
}

func documents(w http.ResponseWriter, r *http.Request) error {
	if r.Method == "GET" {
		heads, e := rows(`SELECT h.*,COUNT(l.id) line_count,COALESCE(SUM(l.quantity*l.unit_price),0) total FROM document_headers h LEFT JOIN document_lines l ON l.document_id=h.id GROUP BY h.id ORDER BY h.id DESC LIMIT 2000`)
		if e != nil {
			return e
		}
		ls, e := rows(`SELECT l.*,i.sku,i.name item,i.unit,COALESCE(p.received,0) received,COALESCE(p.status,'') order_status FROM document_lines l JOIN items i ON i.id=l.item_id JOIN document_headers h ON h.id=l.document_id LEFT JOIN purchase_orders p ON h.kind='PO' AND p.id=l.legacy_id ORDER BY l.id`)
		if e != nil {
			return e
		}
		respond(w, M{"headers": heads, "lines": ls})
		return nil
	}
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b documentInput
	if e := decode(r, &b); e != nil {
		return e
	}
	b.Party = strings.TrimSpace(b.Party)
	b.Note = strings.TrimSpace(b.Note)
	if len(b.Lines) == 0 || len(b.Lines) > 50 {
		return bad("Add between 1 and 50 detail lines.")
	}
	if len(b.Party) > 150 || len(b.Note) > 255 {
		return bad("Header text is too long.")
	}
	if b.Kind != "PR" && b.Kind != "PO" && b.Kind != "GR" && b.Kind != "GI" && b.Kind != "SALE" {
		return bad("Invalid document type.")
	}
	if b.Kind == "PR" && b.Note == "" {
		return bad("Enter a reason for the purchase request.")
	}
	if b.Kind == "PO" && b.Party == "" {
		return bad("Enter a supplier.")
	}
	if (b.Kind == "PO" || b.Kind == "GR") && b.ReferenceID < 1 {
		return bad("Select the source document.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = lockPeriod(tx, true); e != nil {
		return e
	}
	if b.Kind == "PO" || b.Kind == "GR" {
		var k, status string
		if e = tx.QueryRow("SELECT kind,status FROM document_headers WHERE id=? FOR UPDATE", b.ReferenceID).Scan(&k, &status); e != nil {
			return bad("Source document not found.")
		}
		if b.Kind == "PO" && (k != "PR" || status != "approved") {
			return bad("Select an approved purchase request.")
		}
		if b.Kind == "GR" && (k != "PO" || status == "received") {
			return bad("Select an open purchase order.")
		}
	}
	status := "posted"
	if b.Kind == "PR" {
		status = "pending"
	}
	if b.Kind == "PO" {
		status = "open"
	}
	var ref any
	if b.ReferenceID > 0 {
		ref = b.ReferenceID
	}
	result, e := tx.Exec("INSERT INTO document_headers(kind,status,reference_id,party,note) VALUES(?,?,?,?,?)", b.Kind, status, ref, b.Party, b.Note)
	if e != nil {
		return e
	}
	docID, e := result.LastInsertId()
	if e != nil {
		return e
	}
	seen := map[int64]bool{}
	for _, line := range b.Lines {
		if line.ItemID < 1 || !validPositive(line.Quantity) {
			return bad("Each line needs an item and a positive quantity.")
		}
		if (b.Kind == "PR" || b.Kind == "PO" || b.Kind == "SALE") && !validPositive(line.UnitPrice) {
			return bad("Each line needs a positive unit price or cost.")
		}
		if (b.Kind == "PO" || b.Kind == "GR") && (line.SourceID < 1 || seen[line.SourceID]) {
			return bad("Each source line may appear only once.")
		}
		seen[line.SourceID] = true
		var legacy int64
		price := line.UnitPrice
		switch b.Kind {
		case "PR":
			res, e := tx.Exec("INSERT INTO purchase_requests(item_id,quantity,estimated_cost,reason) VALUES(?,?,?,?)", line.ItemID, line.Quantity, price, b.Note)
			if e != nil {
				return bad("Invalid item or request line.")
			}
			legacy, e = res.LastInsertId()
			if e != nil {
				return e
			}
		case "PO":
			var item int64
			var requested float64
			var state string
			e = tx.QueryRow(`SELECT p.item_id,p.quantity,p.status FROM document_lines l JOIN purchase_requests p ON p.id=l.legacy_id WHERE l.id=? AND l.document_id=? FOR UPDATE`, line.SourceID, b.ReferenceID).Scan(&item, &requested, &state)
			if e != nil || state != "approved" || item != line.ItemID {
				return bad("Invalid or unapproved request line.")
			}
			if money(line.Quantity) != money(requested) {
				return bad("Order quantity must match the requested line; splitting a request line across orders is not supported.")
			}
			res, e := tx.Exec("INSERT INTO purchase_orders(request_id,item_id,quantity,unit_cost,supplier) SELECT legacy_id,?,?,?,? FROM document_lines WHERE id=?", item, line.Quantity, price, b.Party, line.SourceID)
			if e != nil {
				return bad("A purchase order already exists for a selected request line.")
			}
			legacy, e = res.LastInsertId()
			if e != nil {
				return e
			}
		case "GR":
			var order, item int64
			var ordered, received, cost float64
			var state string
			e = tx.QueryRow(`SELECT p.id,p.item_id,p.quantity,p.received,p.unit_cost,p.status FROM document_lines l JOIN purchase_orders p ON p.id=l.legacy_id WHERE l.id=? AND l.document_id=? FOR UPDATE`, line.SourceID, b.ReferenceID).Scan(&order, &item, &ordered, &received, &cost, &state)
			if e != nil || item != line.ItemID || state != "open" {
				return bad("Select an open order line.")
			}
			if line.Quantity > ordered-received+0.00001 {
				return bad("Receipt exceeds the outstanding quantity.")
			}
			var qty, avg float64
			if e = tx.QueryRow("SELECT quantity,average_cost FROM items WHERE id=? FOR UPDATE", item).Scan(&qty, &avg); e != nil {
				return e
			}
			avg = money((qty*avg + line.Quantity*cost) / (qty + line.Quantity))
			if _, e = tx.Exec("UPDATE items SET quantity=?,average_cost=? WHERE id=?", qty+line.Quantity, avg, item); e != nil {
				return e
			}
			received = money(received + line.Quantity)
			orderStatus := "open"
			if received >= ordered {
				orderStatus = "received"
			}
			if _, e = tx.Exec("UPDATE purchase_orders SET received=?,status=? WHERE id=?", received, orderStatus, order); e != nil {
				return e
			}
			res, e := tx.Exec("INSERT INTO movements(item_id,kind,quantity,unit_cost,note) VALUES(?,'GR',?,?,?)", item, line.Quantity, cost, fmt.Sprintf("Purchase order #%d", order))
			if e != nil {
				return e
			}
			legacy, e = res.LastInsertId()
			if e != nil {
				return e
			}
			price = cost
		case "GI", "SALE":
			var qty, cost float64
			if e = tx.QueryRow("SELECT quantity,average_cost FROM items WHERE id=? FOR UPDATE", line.ItemID).Scan(&qty, &cost); e != nil {
				return bad("Item not found.")
			}
			if line.Quantity > qty {
				return bad("Insufficient stock for one of the lines; nothing was posted.")
			}
			if _, e = tx.Exec("UPDATE items SET quantity=quantity-? WHERE id=?", line.Quantity, line.ItemID); e != nil {
				return e
			}
			var saleID any
			if b.Kind == "SALE" {
				res, e := tx.Exec("INSERT INTO sales(item_id,quantity,unit_price,unit_cost,customer) VALUES(?,?,?,?,?)", line.ItemID, line.Quantity, price, cost, b.Party)
				if e != nil {
					return e
				}
				id, e := res.LastInsertId()
				if e != nil {
					return e
				}
				saleID = id
			}
			res, e := tx.Exec("INSERT INTO movements(item_id,kind,quantity,unit_cost,note,sale_id) VALUES(?,?,?,?,?,?)", line.ItemID, b.Kind, -line.Quantity, cost, b.Note, saleID)
			if e != nil {
				return e
			}
			legacy, e = res.LastInsertId()
			if e != nil {
				return e
			}
			if b.Kind == "GI" {
				price = cost
			}
		}
		var source any
		if line.SourceID > 0 {
			source = line.SourceID
		}
		if _, e = tx.Exec("INSERT INTO document_lines(document_id,item_id,quantity,unit_price,source_id,legacy_id) VALUES(?,?,?,?,?,?)", docID, line.ItemID, line.Quantity, price, source, legacy); e != nil {
			return e
		}
	}
	if b.Kind == "GR" {
		var outstanding int
		if e = tx.QueryRow(`SELECT COUNT(*) FROM document_lines l JOIN purchase_orders p ON p.id=l.legacy_id WHERE l.document_id=? AND p.status='open'`, b.ReferenceID).Scan(&outstanding); e != nil {
			return e
		}
		if outstanding == 0 {
			if _, e = tx.Exec("UPDATE document_headers SET status='received' WHERE id=?", b.ReferenceID); e != nil {
				return e
			}
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true, "id": docID})
	return nil
}
func documentDecision(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		ID       int64  `json:"id"`
		Decision string `json:"decision"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if b.Decision != "approved" && b.Decision != "rejected" {
		return bad("Invalid decision.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.Exec("UPDATE document_headers SET status=? WHERE id=? AND kind='PR' AND status='pending'", b.Decision, b.ID)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return bad("Request not found or already decided.")
	}
	if _, e = tx.Exec(`UPDATE purchase_requests p JOIN document_lines l ON l.legacy_id=p.id SET p.status=? WHERE l.document_id=? AND p.status='pending'`, b.Decision, b.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true})
	return nil
}
func topItems(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "GET"); e != nil {
		return e
	}
	p, e := rows(`SELECT i.name,i.sku,i.unit,SUM(m.quantity) quantity FROM movements m JOIN items i ON i.id=m.item_id WHERE m.kind='GR' GROUP BY i.id,i.name,i.sku,i.unit ORDER BY quantity DESC LIMIT 5`)
	if e != nil {
		return e
	}
	s, e := rows(`SELECT i.name,i.sku,i.unit,SUM(s.quantity) quantity FROM sales s JOIN items i ON i.id=s.item_id GROUP BY i.id,i.name,i.sku,i.unit ORDER BY quantity DESC LIMIT 5`)
	if e != nil {
		return e
	}
	respond(w, M{"purchased": p, "sold": s})
	return nil
}
