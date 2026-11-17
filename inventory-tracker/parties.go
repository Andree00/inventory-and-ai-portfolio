package main

import (
	"fmt"
	"net/http"
	"strings"
)

func migrateParties() error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS customers(id BIGINT AUTO_INCREMENT PRIMARY KEY,name VARCHAR(150) NOT NULL UNIQUE,phone VARCHAR(60) NOT NULL DEFAULT '',email VARCHAR(150) NOT NULL DEFAULT '',address VARCHAR(255) NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS vendors(id BIGINT AUTO_INCREMENT PRIMARY KEY,name VARCHAR(150) NOT NULL UNIQUE,phone VARCHAR(60) NOT NULL DEFAULT '',email VARCHAR(150) NOT NULL DEFAULT '',address VARCHAR(255) NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS app_migrations(name VARCHAR(100) PRIMARY KEY);`)
	if e != nil {
		return e
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRow("SELECT COUNT(*) FROM app_migrations WHERE name='party_master_seed_v1'").Scan(&n); e != nil {
		return e
	}
	if n == 0 {
		for _, q := range []string{
			`INSERT IGNORE INTO vendors(name) SELECT DISTINCT TRIM(supplier) FROM purchase_orders WHERE TRIM(supplier)<>''`,
			`INSERT IGNORE INTO vendors(name) SELECT DISTINCT TRIM(party) FROM document_headers WHERE kind='PO' AND TRIM(party)<>''`,
			`INSERT IGNORE INTO customers(name) SELECT DISTINCT TRIM(customer) FROM sales WHERE TRIM(customer)<>''`,
			`INSERT IGNORE INTO customers(name) SELECT DISTINCT TRIM(party) FROM document_headers WHERE kind='SALE' AND TRIM(party)<>''`,
			`INSERT INTO app_migrations(name) VALUES('party_master_seed_v1')`,
		} {
			if _, e = tx.Exec(q); e != nil {
				return e
			}
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	for _, table := range []string{"customers", "vendors"} {
		fields := "'name',%s.name,'phone',%s.phone,'email',%s.email,'address',%s.address"
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			before, after, id := "NULL", "JSON_OBJECT("+strings.ReplaceAll(fields, "%s", "NEW")+")", "NEW.id"
			if op != "INSERT" {
				before = "JSON_OBJECT(" + strings.ReplaceAll(fields, "%s", "OLD") + ")"
			}
			if op == "DELETE" {
				after, id = "NULL", "OLD.id"
			}
			q := fmt.Sprintf("CREATE TRIGGER IF NOT EXISTS history_%s_%s AFTER %s ON %s FOR EACH ROW INSERT INTO transaction_history(entity_type,entity_id,action,before_data,after_data) VALUES('%s',%s,'%s',%s,%s)", table, strings.ToLower(op), op, table, table, id, op, before, after)
			if _, e = db.Exec(q); e != nil {
				return e
			}
		}
		q := fmt.Sprintf("INSERT INTO transaction_history(entity_type,entity_id,action,after_data) SELECT '%s',x.id,'BASELINE',JSON_OBJECT(%s) FROM %s x WHERE NOT EXISTS (SELECT 1 FROM transaction_history h WHERE h.entity_type='%s' AND h.entity_id=x.id)", table, strings.ReplaceAll(fields, "%s", "x"), table, table)
		if _, e = db.Exec(q); e != nil {
			return e
		}
	}
	return nil
}

func parties(table string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if r.Method == "GET" {
			x, e := rows("SELECT id,name,phone,email,address FROM " + table + " ORDER BY name")
			if e != nil {
				return e
			}
			respond(w, x)
			return nil
		}
		if r.Method != "POST" && r.Method != "PUT" {
			return appError{405, "Method not allowed."}
		}
		var b struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Phone   string `json:"phone"`
			Email   string `json:"email"`
			Address string `json:"address"`
		}
		if e := decode(r, &b); e != nil {
			return e
		}
		b.Name = strings.TrimSpace(b.Name)
		b.Phone = strings.TrimSpace(b.Phone)
		b.Email = strings.TrimSpace(b.Email)
		b.Address = strings.TrimSpace(b.Address)
		if b.Name == "" || len(b.Name) > 150 || len(b.Phone) > 60 || len(b.Email) > 150 || len(b.Address) > 255 {
			return bad("Enter a name and keep contact details within their limits.")
		}
		if r.Method == "POST" {
			_, e := db.Exec("INSERT INTO "+table+"(name,phone,email,address) VALUES(?,?,?,?)", b.Name, b.Phone, b.Email, b.Address)
			if e != nil {
				return bad("Could not add record; the name may already exist.")
			}
		} else {
			if b.ID < 1 {
				return bad("A valid ID is required.")
			}
			var n int
			if e := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE id=?", b.ID).Scan(&n); e != nil {
				return e
			}
			if n == 0 {
				return bad("Record not found.")
			}
			if _, e := db.Exec("UPDATE "+table+" SET name=?,phone=?,email=?,address=? WHERE id=?", b.Name, b.Phone, b.Email, b.Address, b.ID); e != nil {
				return bad("Could not update record; the name may already exist.")
			}
		}
		respond(w, M{"ok": true})
		return nil
	}
}

// Search only fixed, explicitly whitelisted sources; user input is a bound parameter.
func searchOptions(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "GET"); e != nil {
		return e
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 150 {
		return bad("Search is too long.")
	}
	resource := r.URL.Query().Get("resource")
	queries := map[string]string{
		"items":      `SELECT CAST(id AS CHAR) value,CONCAT(sku,' · ',name) label FROM items WHERE CONCAT(sku,' ',name) LIKE ? ORDER BY name LIMIT 100`,
		"brands":     `SELECT CAST(id AS CHAR) value,name label FROM brands WHERE name LIKE ? ORDER BY name LIMIT 100`,
		"categories": `SELECT CAST(id AS CHAR) value,name label FROM categories WHERE name LIKE ? ORDER BY name LIMIT 100`,
		"vendors":    `SELECT name value,name label FROM vendors WHERE name LIKE ? ORDER BY name LIMIT 100`,
		"customers":  `SELECT name value,name label FROM customers WHERE name LIKE ? ORDER BY name LIMIT 100`,
		"requests":   `SELECT CAST(p.id AS CHAR) value,CONCAT('#',p.id,' · ',i.name) label FROM purchase_requests p JOIN items i ON i.id=p.item_id WHERE CONCAT(p.id,' ',i.name,' ',i.sku) LIKE ? ORDER BY p.id DESC LIMIT 100`,
		"orders":     `SELECT CAST(p.id AS CHAR) value,CONCAT('#',p.id,' · ',i.name) label FROM purchase_orders p JOIN items i ON i.id=p.item_id WHERE CONCAT(p.id,' ',i.name,' ',i.sku) LIKE ? ORDER BY p.id DESC LIMIT 100`,
		"documents":  `SELECT CAST(id AS CHAR) value,CONCAT(kind,' #',id,' · ',party,' ',note) label FROM document_headers WHERE CONCAT(kind,' ',CASE kind WHEN 'PR' THEN 'Purchase request' WHEN 'PO' THEN 'Purchase order' WHEN 'GR' THEN 'Goods receipt' WHEN 'GI' THEN 'Goods issue' ELSE 'Sale' END,' #',id,' ',party,' ',note) LIKE ? ORDER BY id DESC LIMIT 2000`,
		"lines":      `SELECT CAST(l.id AS CHAR) value,CONCAT(i.sku,' · ',i.name) label FROM document_lines l JOIN items i ON i.id=l.item_id WHERE CONCAT(i.sku,' ',i.name) LIKE ? ORDER BY l.id DESC LIMIT 2000`,
	}
	query, ok := queries[resource]
	if !ok {
		return bad("Invalid search source.")
	}
	x, e := rows(query, "%"+q+"%")
	if e != nil {
		return e
	}
	respond(w, x)
	return nil
}
