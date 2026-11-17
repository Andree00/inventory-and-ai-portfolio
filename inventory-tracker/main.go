package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var db *sql.DB

type M = map[string]any
type appError struct {
	status  int
	message string
}

func (e appError) Error() string { return e.message }
func bad(s string) error         { return appError{400, s} }
func money(v float64) float64    { return math.Round(v*10000) / 10000 }
func validPositive(v float64) bool {
	return v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v) && v <= 100000000
}
func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return bad("Invalid request body.")
	}
	return nil
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func handler(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if e := fn(w, r); e != nil {
			status := 500
			message := "Server error."
			var ae appError
			if errors.As(e, &ae) {
				status = ae.status
				message = ae.message
			} else {
				log.Print(e)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(M{"error": message})
		}
	}
}
func rows(query string, args ...any) ([]M, error) {
	rs, e := db.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rs.Close()
	cols, e := rs.Columns()
	if e != nil {
		return nil, e
	}
	out := []M{}
	for rs.Next() {
		vals := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range vals {
			ptr[i] = &vals[i]
		}
		if e = rs.Scan(ptr...); e != nil {
			return nil, e
		}
		m := M{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = vals[i]
			}
		}
		out = append(out, m)
	}
	return out, rs.Err()
}
func idParam(r *http.Request) (int64, error) {
	id, e := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if e != nil || id < 1 {
		return 0, bad("A valid ID is required.")
	}
	return id, nil
}
func method(r *http.Request, want string) error {
	if r.Method != want {
		return appError{405, "Method not allowed."}
	}
	return nil
}

// Every stock-changing operation locks the open period before item rows.
func lockPeriod(tx *sql.Tx, requireCalendarMonth bool) (string, error) {
	var open, calendar string
	if e := tx.QueryRow(`SELECT period,DATE_FORMAT(CURRENT_DATE(),'%Y-%m') FROM accounting_periods WHERE status='open' FOR UPDATE`).Scan(&open, &calendar); e != nil {
		return "", e
	}
	if requireCalendarMonth && open != calendar {
		return "", bad("The open period is not the current calendar month. Close earlier months before posting stock transactions.")
	}
	return open, nil
}

func period(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "GET"); e != nil {
		return e
	}
	x, e := rows(`SELECT period,status,DATE_FORMAT(CURRENT_DATE(),'%Y-%m') calendar_month,opened_at FROM accounting_periods WHERE status='open'`)
	if e != nil {
		return e
	}
	if len(x) != 1 {
		return appError{500, "No open accounting period is configured."}
	}
	respond(w, x[0])
	return nil
}
func main() {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "inventory:local_inventory_password@tcp(127.0.0.1:3307)/inventory_portfolio?parseTime=true&multiStatements=true"
	}
	var e error
	db, e = sql.Open("mysql", dsn)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	if e = db.Ping(); e != nil {
		log.Fatalf("Cannot connect to MySQL: %v", e)
	}
	if e = migrate(); e != nil {
		log.Fatal(e)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/overview", handler(overview))
	mux.HandleFunc("/api/items", handler(items))
	mux.HandleFunc("/api/brands", handler(lookup("brands")))
	mux.HandleFunc("/api/categories", handler(lookup("categories")))
	mux.HandleFunc("/api/requests", handler(requests))
	mux.HandleFunc("/api/requests/decision", handler(decision))
	mux.HandleFunc("/api/orders", handler(orders))
	mux.HandleFunc("/api/receipts", handler(receipts))
	mux.HandleFunc("/api/issues", handler(issues))
	mux.HandleFunc("/api/sales", handler(sales))
	mux.HandleFunc("/api/movements", handler(movements))
	mux.HandleFunc("/api/reports", handler(reports))
	mux.HandleFunc("/api/history", handler(history))
	mux.HandleFunc("/api/recalculate", handler(recalculate))
	mux.HandleFunc("/api/monthly", handler(monthly))
	mux.HandleFunc("/api/period", handler(period))
	mux.Handle("/", http.FileServer(http.Dir("web")))
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	log.Printf("Inventory tracker: http://%s", addr)
	server := http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
func migrate() error {
	_, e := db.Exec(`
CREATE TABLE IF NOT EXISTS brands(id BIGINT AUTO_INCREMENT PRIMARY KEY,name VARCHAR(100) NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS categories(id BIGINT AUTO_INCREMENT PRIMARY KEY,name VARCHAR(100) NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS items(id BIGINT AUTO_INCREMENT PRIMARY KEY,sku VARCHAR(50) NOT NULL UNIQUE,name VARCHAR(150) NOT NULL,brand_id BIGINT NOT NULL,category_id BIGINT NOT NULL,unit VARCHAR(20) NOT NULL,quantity DECIMAL(16,4) NOT NULL DEFAULT 0,average_cost DECIMAL(16,4) NOT NULL DEFAULT 0,reorder_level DECIMAL(16,4) NOT NULL DEFAULT 0,FOREIGN KEY(brand_id) REFERENCES brands(id),FOREIGN KEY(category_id) REFERENCES categories(id));
CREATE TABLE IF NOT EXISTS purchase_requests(id BIGINT AUTO_INCREMENT PRIMARY KEY,item_id BIGINT NOT NULL,quantity DECIMAL(16,4) NOT NULL,estimated_cost DECIMAL(16,4) NOT NULL,reason VARCHAR(255) NOT NULL,status VARCHAR(20) NOT NULL DEFAULT 'pending',created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(item_id) REFERENCES items(id));
CREATE TABLE IF NOT EXISTS purchase_orders(id BIGINT AUTO_INCREMENT PRIMARY KEY,request_id BIGINT NOT NULL UNIQUE,item_id BIGINT NOT NULL,quantity DECIMAL(16,4) NOT NULL,received DECIMAL(16,4) NOT NULL DEFAULT 0,unit_cost DECIMAL(16,4) NOT NULL,supplier VARCHAR(150) NOT NULL,status VARCHAR(20) NOT NULL DEFAULT 'open',created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(request_id) REFERENCES purchase_requests(id),FOREIGN KEY(item_id) REFERENCES items(id));
CREATE TABLE IF NOT EXISTS movements(id BIGINT AUTO_INCREMENT PRIMARY KEY,item_id BIGINT NOT NULL,kind VARCHAR(20) NOT NULL,quantity DECIMAL(16,4) NOT NULL,unit_cost DECIMAL(16,4) NOT NULL,note VARCHAR(255) NOT NULL,created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(item_id) REFERENCES items(id));
CREATE TABLE IF NOT EXISTS sales(id BIGINT AUTO_INCREMENT PRIMARY KEY,item_id BIGINT NOT NULL,quantity DECIMAL(16,4) NOT NULL,unit_price DECIMAL(16,4) NOT NULL,unit_cost DECIMAL(16,4) NOT NULL,customer VARCHAR(150) NOT NULL,created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(item_id) REFERENCES items(id));
CREATE TABLE IF NOT EXISTS transaction_history(id BIGINT AUTO_INCREMENT PRIMARY KEY,entity_type VARCHAR(40) NOT NULL,entity_id BIGINT NOT NULL,action VARCHAR(20) NOT NULL,before_data JSON NULL,after_data JSON NULL,created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,INDEX(entity_type,entity_id),INDEX(created_at));
CREATE TABLE IF NOT EXISTS monthly_stock_snapshots(id BIGINT AUTO_INCREMENT PRIMARY KEY,period CHAR(7) NOT NULL,item_id BIGINT NOT NULL,quantity DECIMAL(16,4) NOT NULL,unit_cost DECIMAL(16,4) NOT NULL,stock_value DECIMAL(20,4) NOT NULL,captured_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,UNIQUE KEY(period,item_id),FOREIGN KEY(item_id) REFERENCES items(id));
CREATE TABLE IF NOT EXISTS cost_recalculations(id BIGINT AUTO_INCREMENT PRIMARY KEY,item_count INT NOT NULL,movement_count INT NOT NULL,created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS accounting_periods(id BIGINT AUTO_INCREMENT PRIMARY KEY,period CHAR(7) NOT NULL UNIQUE,status VARCHAR(10) NOT NULL DEFAULT 'open',opened_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,closed_at TIMESTAMP NULL);
`)
	if e != nil {
		return e
	}
	var hasSaleID int
	if e = db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='movements' AND column_name='sale_id'`).Scan(&hasSaleID); e != nil {
		return e
	}
	if hasSaleID == 0 {
		if _, e = db.Exec(`ALTER TABLE movements ADD COLUMN sale_id BIGINT NULL, ADD UNIQUE KEY movement_sale_id(sale_id), ADD CONSTRAINT movement_sale_fk FOREIGN KEY(sale_id) REFERENCES sales(id)`); e != nil {
			return e
		}
	}
	if _, e = db.Exec(`INSERT INTO accounting_periods(period,status) SELECT COALESCE((SELECT MIN(DATE_FORMAT(created_at,'%Y-%m')) FROM movements),DATE_FORMAT(CURRENT_DATE(),'%Y-%m')),'open' WHERE NOT EXISTS (SELECT 1 FROM accounting_periods)`); e != nil {
		return e
	}
	// Database triggers record the before and after values in the same transaction.
	definitions := []struct{ table, fields string }{
		{"brands", "'name',%s.name"},
		{"categories", "'name',%s.name"},
		{"items", "'sku',%s.sku,'name',%s.name,'brand_id',%s.brand_id,'category_id',%s.category_id,'unit',%s.unit,'quantity',%s.quantity,'average_cost',%s.average_cost,'reorder_level',%s.reorder_level"},
		{"purchase_requests", "'item_id',%s.item_id,'quantity',%s.quantity,'estimated_cost',%s.estimated_cost,'reason',%s.reason,'status',%s.status"},
		{"purchase_orders", "'request_id',%s.request_id,'item_id',%s.item_id,'quantity',%s.quantity,'received',%s.received,'unit_cost',%s.unit_cost,'supplier',%s.supplier,'status',%s.status"},
		{"movements", "'item_id',%s.item_id,'kind',%s.kind,'quantity',%s.quantity,'unit_cost',%s.unit_cost,'note',%s.note"},
		{"sales", "'item_id',%s.item_id,'quantity',%s.quantity,'unit_price',%s.unit_price,'unit_cost',%s.unit_cost,'customer',%s.customer"},
		{"monthly_stock_snapshots", "'period',%s.period,'item_id',%s.item_id,'quantity',%s.quantity,'unit_cost',%s.unit_cost,'stock_value',%s.stock_value"},
		{"cost_recalculations", "'item_count',%s.item_count,'movement_count',%s.movement_count"},
		{"accounting_periods", "'period',%s.period,'status',%s.status,'closed_at',%s.closed_at"},
	}
	for _, d := range definitions {
		newFields := strings.ReplaceAll(d.fields, "%s", "NEW")
		oldFields := strings.ReplaceAll(d.fields, "%s", "OLD")
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			before := "NULL"
			after := "JSON_OBJECT(" + newFields + ")"
			rowID := "NEW.id"
			if op == "UPDATE" || op == "DELETE" {
				before = "JSON_OBJECT(" + oldFields + ")"
			}
			if op == "DELETE" {
				after = "NULL"
				rowID = "OLD.id"
			}
			q := fmt.Sprintf("CREATE TRIGGER IF NOT EXISTS history_%s_%s AFTER %s ON %s FOR EACH ROW INSERT INTO transaction_history(entity_type,entity_id,action,before_data,after_data) VALUES('%s',%s,'%s',%s,%s)", d.table, strings.ToLower(op), op, d.table, d.table, rowID, op, before, after)
			if _, e = db.Exec(q); e != nil {
				return e
			}
		}
		// Existing records get a baseline snapshot; past changes cannot be reconstructed.
		baseline := strings.ReplaceAll(d.fields, "%s", "x")
		q := fmt.Sprintf("INSERT INTO transaction_history(entity_type,entity_id,action,after_data) SELECT '%s',x.id,'BASELINE',JSON_OBJECT(%s) FROM %s x WHERE NOT EXISTS (SELECT 1 FROM transaction_history h WHERE h.entity_type='%s' AND h.entity_id=x.id)", d.table, baseline, d.table, d.table)
		if _, e = db.Exec(q); e != nil {
			return e
		}
	}
	return nil
}
func lookup(table string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if r.Method == "GET" {
			x, e := rows("SELECT id,name FROM " + table + " ORDER BY name")
			if e != nil {
				return e
			}
			respond(w, x)
			return nil
		}
		if e := method(r, "POST"); e != nil {
			return e
		}
		var b struct {
			Name string `json:"name"`
		}
		if e := decode(r, &b); e != nil {
			return e
		}
		b.Name = strings.TrimSpace(b.Name)
		if len(b.Name) < 1 || len(b.Name) > 100 {
			return bad("Enter a name of up to 100 characters.")
		}
		_, e := db.Exec("INSERT INTO "+table+"(name) VALUES(?)", b.Name)
		if e != nil {
			return bad("Could not add name; it may already exist.")
		}
		respond(w, M{"ok": true})
		return nil
	}
}
func items(w http.ResponseWriter, r *http.Request) error {
	if r.Method == "GET" {
		x, e := rows(`SELECT i.*,b.name brand,c.name category FROM items i JOIN brands b ON b.id=i.brand_id JOIN categories c ON c.id=i.category_id ORDER BY i.id DESC`)
		if e != nil {
			return e
		}
		respond(w, x)
		return nil
	}
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		SKU        string  `json:"sku"`
		Name       string  `json:"name"`
		BrandID    int     `json:"brand_id"`
		CategoryID int     `json:"category_id"`
		Unit       string  `json:"unit"`
		Reorder    float64 `json:"reorder_level"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	b.SKU = strings.TrimSpace(b.SKU)
	b.Name = strings.TrimSpace(b.Name)
	b.Unit = strings.TrimSpace(b.Unit)
	if b.SKU == "" || b.Name == "" || b.Unit == "" || b.BrandID < 1 || b.CategoryID < 1 || b.Reorder < 0 {
		return bad("Complete all item fields and choose valid master data.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = lockPeriod(tx, true); e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO items(sku,name,brand_id,category_id,unit,reorder_level) VALUES(?,?,?,?,?,?)", b.SKU, b.Name, b.BrandID, b.CategoryID, b.Unit, b.Reorder)
	if e != nil {
		return bad("Could not create item. Check SKU and master data.")
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true})
	return nil
}
func requests(w http.ResponseWriter, r *http.Request) error {
	if r.Method == "GET" {
		x, e := rows(`SELECT p.*,i.sku,i.name item FROM purchase_requests p JOIN items i ON i.id=p.item_id ORDER BY p.id DESC`)
		if e != nil {
			return e
		}
		respond(w, x)
		return nil
	}
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		ItemID   int     `json:"item_id"`
		Quantity float64 `json:"quantity"`
		Cost     float64 `json:"estimated_cost"`
		Reason   string  `json:"reason"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if b.ItemID < 1 || !validPositive(b.Quantity) || !validPositive(b.Cost) || strings.TrimSpace(b.Reason) == "" {
		return bad("Enter an item, positive quantity and cost, and a reason.")
	}
	_, e := db.Exec("INSERT INTO purchase_requests(item_id,quantity,estimated_cost,reason) VALUES(?,?,?,?)", b.ItemID, b.Quantity, b.Cost, b.Reason)
	if e != nil {
		return bad("Could not create request. Check the item.")
	}
	respond(w, M{"ok": true})
	return nil
}
func decision(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		ID       int    `json:"id"`
		Decision string `json:"decision"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if b.Decision != "approved" && b.Decision != "rejected" {
		return bad("Choose approve or reject.")
	}
	res, e := db.Exec("UPDATE purchase_requests SET status=? WHERE id=? AND status='pending'", b.Decision, b.ID)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return bad("Request not found or already decided.")
	}
	respond(w, M{"ok": true})
	return nil
}
func orders(w http.ResponseWriter, r *http.Request) error {
	if r.Method == "GET" {
		x, e := rows(`SELECT p.*,i.sku,i.name item FROM purchase_orders p JOIN items i ON i.id=p.item_id ORDER BY p.id DESC`)
		if e != nil {
			return e
		}
		respond(w, x)
		return nil
	}
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		RequestID int    `json:"request_id"`
		Supplier  string `json:"supplier"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	b.Supplier = strings.TrimSpace(b.Supplier)
	if b.RequestID < 1 || b.Supplier == "" {
		return bad("Choose an approved request and enter a supplier.")
	}
	res, e := db.Exec(`INSERT INTO purchase_orders(request_id,item_id,quantity,unit_cost,supplier) SELECT id,item_id,quantity,estimated_cost,? FROM purchase_requests WHERE id=? AND status='approved'`, b.Supplier, b.RequestID)
	if e != nil {
		return bad("A purchase order may already exist for this request.")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return bad("Only approved requests can become purchase orders.")
	}
	respond(w, M{"ok": true})
	return nil
}
func receipts(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		OrderID  int     `json:"order_id"`
		Quantity float64 `json:"quantity"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if b.OrderID < 1 || !validPositive(b.Quantity) {
		return bad("Enter a valid order and positive quantity.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = lockPeriod(tx, true); e != nil {
		return e
	}
	var item int
	var qty, received, cost float64
	var status string
	e = tx.QueryRow("SELECT item_id,quantity,received,unit_cost,status FROM purchase_orders WHERE id=? FOR UPDATE", b.OrderID).Scan(&item, &qty, &received, &cost, &status)
	if e != nil {
		return bad("Purchase order not found.")
	}
	if status != "open" || b.Quantity > qty-received+0.00001 {
		return bad("Receipt exceeds the outstanding order quantity.")
	}
	var stock, average float64
	if e = tx.QueryRow("SELECT quantity,average_cost FROM items WHERE id=? FOR UPDATE", item).Scan(&stock, &average); e != nil {
		return e
	}
	newCost := money((stock*average + b.Quantity*cost) / (stock + b.Quantity))
	if _, e = tx.Exec("UPDATE items SET quantity=?,average_cost=? WHERE id=?", stock+b.Quantity, newCost, item); e != nil {
		return e
	}
	newReceived := money(received + b.Quantity)
	if _, e = tx.Exec("UPDATE purchase_orders SET received=?,status=? WHERE id=?", newReceived, map[bool]string{true: "received", false: "open"}[newReceived >= qty], b.OrderID); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO movements(item_id,kind,quantity,unit_cost,note) VALUES(?,'GR',?,?,?)", item, b.Quantity, cost, fmt.Sprintf("Purchase order #%d", b.OrderID)); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true})
	return nil
}
func stockOut(w http.ResponseWriter, r *http.Request, isSale bool) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	var b struct {
		ItemID   int     `json:"item_id"`
		Quantity float64 `json:"quantity"`
		Price    float64 `json:"unit_price"`
		Note     string  `json:"note"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if b.ItemID < 1 || !validPositive(b.Quantity) || (isSale && !validPositive(b.Price)) {
		return bad("Enter an item and positive quantity and price where applicable.")
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = lockPeriod(tx, true); e != nil {
		return e
	}
	var stock, cost float64
	if e = tx.QueryRow("SELECT quantity,average_cost FROM items WHERE id=? FOR UPDATE", b.ItemID).Scan(&stock, &cost); e != nil {
		return bad("Item not found.")
	}
	if b.Quantity > stock {
		return bad("Insufficient stock.")
	}
	if _, e = tx.Exec("UPDATE items SET quantity=quantity-? WHERE id=?", b.Quantity, b.ItemID); e != nil {
		return e
	}
	kind := "GI"
	var saleID any
	if isSale {
		kind = "SALE"
		var result sql.Result
		if result, e = tx.Exec("INSERT INTO sales(item_id,quantity,unit_price,unit_cost,customer) VALUES(?,?,?,?,?)", b.ItemID, b.Quantity, b.Price, cost, b.Note); e != nil {
			return e
		}
		saleID, e = result.LastInsertId()
		if e != nil {
			return e
		}
	}
	if _, e = tx.Exec("INSERT INTO movements(item_id,kind,quantity,unit_cost,note,sale_id) VALUES(?,?,?,?,?,?)", b.ItemID, kind, -b.Quantity, cost, b.Note, saleID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true, "unit_cost": cost})
	return nil
}
func issues(w http.ResponseWriter, r *http.Request) error { return stockOut(w, r, false) }
func sales(w http.ResponseWriter, r *http.Request) error {
	if r.Method == "GET" {
		x, e := rows(`SELECT s.*,i.sku,i.name item,(s.quantity*s.unit_price) revenue,(s.quantity*s.unit_cost) cogs FROM sales s JOIN items i ON i.id=s.item_id ORDER BY s.id DESC LIMIT 100`)
		if e != nil {
			return e
		}
		respond(w, x)
		return nil
	}
	return stockOut(w, r, true)
}
func movements(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "GET"); e != nil {
		return e
	}
	x, e := rows(`SELECT m.*,i.sku,i.name item FROM movements m JOIN items i ON i.id=m.item_id ORDER BY m.id DESC LIMIT 100`)
	if e != nil {
		return e
	}
	respond(w, x)
	return nil
}
func overview(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "GET"); e != nil {
		return e
	}
	x, e := rows(`SELECT (SELECT COUNT(*) FROM items) items,(SELECT COUNT(*) FROM items WHERE quantity<=reorder_level) low_stock,(SELECT COALESCE(SUM(quantity*average_cost),0) FROM items) stock_value,(SELECT COUNT(*) FROM purchase_requests WHERE status='pending') pending,(SELECT COALESCE(SUM(quantity*unit_price),0) FROM sales) revenue,(SELECT COALESCE(SUM(quantity*unit_cost),0) FROM sales) cogs`)
	if e != nil {
		return e
	}
	respond(w, x[0])
	return nil
}
func reports(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "GET"); e != nil {
		return e
	}
	sales, e := rows(`SELECT DATE_FORMAT(created_at,'%Y-%m-%d') day, SUM(quantity*unit_price) revenue,SUM(quantity*unit_cost) cogs FROM sales GROUP BY day ORDER BY day DESC LIMIT 14`)
	if e != nil {
		return e
	}
	stock, e := rows(`SELECT sku,name,quantity,average_cost,(quantity*average_cost) stock_value FROM items ORDER BY stock_value DESC LIMIT 8`)
	if e != nil {
		return e
	}
	respond(w, M{"sales": sales, "stock": stock})
	return nil
}

func history(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "GET"); e != nil {
		return e
	}
	x, e := rows(`SELECT id,entity_type,entity_id,action,before_data,after_data,created_at FROM transaction_history ORDER BY id DESC LIMIT 200`)
	if e != nil {
		return e
	}
	respond(w, x)
	return nil
}

// Replays movements in their recorded order. Receipt costs are inputs; outbound
// costs are recomputed. Any discrepancy aborts the entire transaction.
func recalculate(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "POST"); e != nil {
		return e
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	open, e := lockPeriod(tx, false)
	if e != nil {
		return e
	}
	type itemRow struct {
		id       int64
		quantity float64
	}
	var items []itemRow
	rs, e := tx.Query("SELECT id,quantity FROM items ORDER BY id FOR UPDATE")
	if e != nil {
		return e
	}
	for rs.Next() {
		var x itemRow
		if e = rs.Scan(&x.id, &x.quantity); e != nil {
			rs.Close()
			return e
		}
		items = append(items, x)
	}
	if e = rs.Err(); e != nil {
		rs.Close()
		return e
	}
	rs.Close()
	type movement struct {
		id             int64
		kind           string
		quantity, cost float64
		saleID         sql.NullInt64
	}
	total := 0
	for _, item := range items {
		mr, e := tx.Query("SELECT id,kind,quantity,unit_cost,sale_id FROM movements WHERE item_id=? AND created_at>=STR_TO_DATE(CONCAT(?,'-01'),'%Y-%m-%d') AND created_at<DATE_ADD(STR_TO_DATE(CONCAT(?,'-01'),'%Y-%m-%d'),INTERVAL 1 MONTH) ORDER BY id FOR UPDATE", item.id, open, open)
		if e != nil {
			return e
		}
		var movements []movement
		for mr.Next() {
			var m movement
			if e = mr.Scan(&m.id, &m.kind, &m.quantity, &m.cost, &m.saleID); e != nil {
				mr.Close()
				return e
			}
			movements = append(movements, m)
		}
		if e = mr.Err(); e != nil {
			mr.Close()
			return e
		}
		mr.Close()
		quantity, cost := 0.0, 0.0
		e = tx.QueryRow(`SELECT s.quantity,s.unit_cost FROM monthly_stock_snapshots s JOIN accounting_periods p ON p.period=s.period AND p.status='closed' WHERE s.item_id=? AND s.period<? ORDER BY s.period DESC LIMIT 1`, item.id, open).Scan(&quantity, &cost)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		for _, m := range movements {
			total++
			switch m.kind {
			case "GR":
				if m.quantity <= 0 || m.cost <= 0 {
					return bad(fmt.Sprintf("Invalid receipt #%d; recalculation cancelled.", m.id))
				}
				cost = money((quantity*cost + m.quantity*m.cost) / (quantity + m.quantity))
				quantity = money(quantity + m.quantity)
			case "GI", "SALE":
				if m.quantity >= 0 || quantity+m.quantity < -0.00001 {
					return bad(fmt.Sprintf("Movement #%d would make stock negative; recalculation cancelled.", m.id))
				}
				if m.kind == "SALE" && !m.saleID.Valid {
					return bad(fmt.Sprintf("Sale movement #%d has no linked sale. Reconcile it before recalculating.", m.id))
				}
				quantity = money(quantity + m.quantity)
				if _, e = tx.Exec("UPDATE movements SET unit_cost=? WHERE id=? AND unit_cost<>?", cost, m.id, cost); e != nil {
					return e
				}
				if m.saleID.Valid {
					result, e := tx.Exec("UPDATE sales SET unit_cost=? WHERE id=? AND item_id=? AND quantity=?", cost, m.saleID.Int64, item.id, -m.quantity)
					if e != nil {
						return e
					}
					n, _ := result.RowsAffected()
					if n == 0 {
						var exists int
						if e = tx.QueryRow("SELECT COUNT(*) FROM sales WHERE id=? AND item_id=? AND quantity=?", m.saleID.Int64, item.id, -m.quantity).Scan(&exists); e != nil || exists != 1 {
							return bad(fmt.Sprintf("Sale link for movement #%d is invalid.", m.id))
						}
					}
				}
			default:
				return bad(fmt.Sprintf("Unknown movement type for #%d; recalculation cancelled.", m.id))
			}
		}
		if math.Abs(quantity-item.quantity) > 0.00001 {
			return bad(fmt.Sprintf("Stock for item #%d does not match its movement history; recalculation cancelled.", item.id))
		}
		if _, e = tx.Exec("UPDATE items SET average_cost=? WHERE id=? AND average_cost<>?", cost, item.id, cost); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("INSERT INTO cost_recalculations(item_count,movement_count) VALUES(?,?)", len(items), total); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true, "items": len(items), "movements": total})
	return nil
}

func monthly(w http.ResponseWriter, r *http.Request) error {
	if r.Method == "GET" {
		x, e := rows(`SELECT s.*,i.sku,i.name item FROM monthly_stock_snapshots s JOIN items i ON i.id=s.item_id ORDER BY s.period DESC,i.sku`)
		if e != nil {
			return e
		}
		respond(w, x)
		return nil
	}
	if e := method(r, "POST"); e != nil {
		return e
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	period, e := lockPeriod(tx, false)
	if e != nil {
		return e
	}
	var calendar string
	if e = tx.QueryRow("SELECT DATE_FORMAT(CURRENT_DATE(),'%Y-%m')").Scan(&calendar); e != nil {
		return e
	}
	if period >= calendar {
		return bad("A period can only be closed after its calendar month ends.")
	}
	type row struct {
		id             int64
		quantity, cost float64
	}
	var entries []row
	rs, e := tx.Query("SELECT id,quantity,average_cost FROM items ORDER BY id FOR UPDATE")
	if e != nil {
		return e
	}
	for rs.Next() {
		var x row
		if e = rs.Scan(&x.id, &x.quantity, &x.cost); e != nil {
			rs.Close()
			return e
		}
		entries = append(entries, x)
	}
	if e = rs.Err(); e != nil {
		rs.Close()
		return e
	}
	rs.Close()
	for _, x := range entries {
		_, e = tx.Exec(`INSERT INTO monthly_stock_snapshots(period,item_id,quantity,unit_cost,stock_value) VALUES(?,?,?,?,?)`, period, x.id, x.quantity, x.cost, money(x.quantity*x.cost))
		if e != nil {
			return e
		}
	}
	if _, e = tx.Exec("UPDATE accounting_periods SET status='closed',closed_at=CURRENT_TIMESTAMP WHERE period=? AND status='open'", period); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO accounting_periods(period,status) VALUES(DATE_FORMAT(DATE_ADD(STR_TO_DATE(CONCAT(?,'-01'),'%Y-%m-%d'),INTERVAL 1 MONTH),'%Y-%m'),'open')`, period); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, M{"ok": true, "period": period, "items": len(entries)})
	return nil
}
