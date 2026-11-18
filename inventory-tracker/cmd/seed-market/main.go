// Command seed-market adds a consistent, fictional Indonesian office-supply dataset.
// It is opt-in, transactional and records an installation marker to prevent reruns.
package main

import (
	"database/sql"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"log"
	"math"
	"math/rand"
	"os"
	"time"
)

type product struct {
	category, brand, name, unit string
	retail                      int
	variants                    []string
}

var catalog = []product{
	{"Paper", "Nusa Paper", "Copy paper", "ream", 45000, []string{"A4 70 gsm", "A4 75 gsm", "A4 80 gsm", "F4 70 gsm", "F4 75 gsm", "F4 80 gsm", "A4 70 gsm bright white", "A4 80 gsm premium", "A4 75 gsm office", "F4 75 gsm office"}},
	{"Notebooks", "Aruna Stationery", "Spiral notebook", "pcs", 24000, []string{"A5 ruled black", "A5 ruled blue", "A5 ruled grey", "A4 ruled black", "A4 ruled blue", "A4 grid black", "B5 ruled blue", "B5 ruled grey", "A5 plain kraft", "A4 plain kraft"}},
	{"Writing instruments", "Cakra Office", "Ballpoint pen", "pcs", 5500, []string{"0.5 mm black", "0.5 mm blue", "0.5 mm red", "0.7 mm black", "0.7 mm blue", "0.7 mm red", "1.0 mm black", "1.0 mm blue", "retractable black", "retractable blue"}},
	{"Writing instruments", "Cakra Office", "Permanent marker", "pcs", 13500, []string{"fine black", "fine blue", "fine red", "medium black", "medium blue", "medium red", "chisel black", "chisel blue", "chisel red", "round black"}},
	{"Filing", "Merapi Filing", "Lever arch file", "pcs", 42000, []string{"A4 black", "A4 blue", "A4 grey", "A4 red", "A4 green", "F4 black", "F4 blue", "F4 grey", "F4 red", "F4 green"}},
	{"Desk supplies", "Sagara Desk", "Desktop stapler", "pcs", 27000, []string{"No.10 black", "No.10 blue", "No.10 white", "No.10 grey", "No.10 red", "No.10 compact black", "No.10 compact blue", "No.10 compact white", "No.10 compact grey", "No.10 compact red"}},
	{"Desk supplies", "Sagara Desk", "Sticky notes", "pack", 18000, []string{"75 mm yellow", "75 mm pink", "75 mm blue", "75 mm green", "50 mm yellow", "50 mm pink", "50 mm blue", "50 mm green", "75 mm pastel", "50 mm pastel"}},
	{"Packing", "Nusa Packaging", "Packing tape", "roll", 14500, []string{"48 mm clear", "48 mm brown", "48 mm white", "24 mm clear", "24 mm brown", "24 mm white", "48 mm reinforced", "24 mm reinforced", "48 mm low-noise", "24 mm low-noise"}},
	{"Packing", "Nusa Packaging", "Document envelope", "pack", 25000, []string{"A4 white 25 pcs", "A4 kraft 25 pcs", "F4 white 25 pcs", "F4 kraft 25 pcs", "A5 white 25 pcs", "A5 kraft 25 pcs", "C4 white 25 pcs", "C4 kraft 25 pcs", "DL white 25 pcs", "DL kraft 25 pcs"}},
	{"Computer accessories", "Langit Digital", "USB flash drive", "pcs", 100000, []string{"32 GB black", "32 GB blue", "32 GB silver", "64 GB black", "64 GB blue", "64 GB silver", "128 GB black", "128 GB blue", "128 GB silver", "64 GB compact"}},
	{"Computer accessories", "Langit Digital", "Wireless mouse", "pcs", 130000, []string{"black", "white", "grey", "blue", "black silent", "white silent", "grey silent", "blue silent", "black compact", "grey compact"}},
	{"Computer accessories", "Langit Digital", "USB keyboard", "pcs", 210000, []string{"black full size", "white full size", "grey full size", "black compact", "white compact", "grey compact", "black slim", "white slim", "grey slim", "black ergonomic"}},
	{"Office furniture", "Ruang Workspace", "Mesh office chair", "pcs", 1200000, []string{"black low back", "black high back", "grey low back", "grey high back", "blue low back", "blue high back", "black adjustable", "grey adjustable", "blue adjustable", "black headrest"}},
	{"Office furniture", "Ruang Workspace", "Office desk", "pcs", 1400000, []string{"100 cm white", "100 cm oak", "100 cm black", "120 cm white", "120 cm oak", "120 cm black", "140 cm white", "140 cm oak", "140 cm black", "120 cm grey"}},
	{"Printer supplies", "Tirta Print", "Ink refill bottle", "pcs", 150000, []string{"black 70 ml", "cyan 70 ml", "magenta 70 ml", "yellow 70 ml", "black 100 ml", "cyan 100 ml", "magenta 100 ml", "yellow 100 ml", "black 127 ml", "cyan 127 ml"}},
}

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func id(r sql.Result, e error) int64 { must(e); v, e := r.LastInsertId(); must(e); return v }
func price(n float64) float64 {
	step := 100.0
	if n >= 100000 {
		step = 1000
	}
	return math.Round(n/step) * step
}
func main() {
	if len(os.Args) != 2 || (os.Args[1] != "--apply" && os.Args[1] != "--demo") {
		log.Fatal("Use --apply to add the dataset to the configured local database. Back up the database first.")
	}
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "inventory:example_inventory_only@tcp(127.0.0.1:3307)/inventory_portfolio?parseTime=true&multiStatements=true"
	}
	if os.Args[1] == "--demo" {
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil || cfg.DBName != "inventory_portfolio_demo" {
			log.Fatal("--demo requires the separate inventory_portfolio_demo database")
		}
	}
	db, e := sql.Open("mysql", dsn)
	must(e)
	defer db.Close()
	tx, e := db.Begin()
	must(e)
	defer tx.Rollback()
	var open, calendar string
	must(tx.QueryRow(`SELECT period,DATE_FORMAT(CURRENT_DATE(),'%Y-%m') FROM accounting_periods WHERE status='open' FOR UPDATE`).Scan(&open, &calendar))
	if open != calendar {
		log.Fatal("Close earlier periods before adding transactions.")
	}
	var n int
	must(tx.QueryRow("SELECT COUNT(*) FROM app_migrations WHERE name='market_inventory_202611_v1'").Scan(&n))
	if n > 0 {
		log.Fatal("This dataset is already installed; no records were added.")
	}
	var conflicts int
	must(tx.QueryRow("SELECT COUNT(*) FROM items WHERE sku LIKE 'IDR-2026-%'").Scan(&conflicts))
	if conflicts > 0 {
		log.Fatal("The sample SKU range is already in use; no records were added.")
	}
	demo := os.Args[1] == "--demo"
	rng := rand.New(rand.NewSource(20261118))
	brands := map[string]int64{}
	categories := map[string]int64{}
	ensure := func(table, name string) int64 {
		_, e := tx.Exec("INSERT IGNORE INTO "+table+"(name) VALUES(?)", name)
		must(e)
		var v int64
		must(tx.QueryRow("SELECT id FROM "+table+" WHERE name=?", name).Scan(&v))
		return v
	}
	for _, p := range catalog {
		if _, ok := brands[p.brand]; !ok {
			brands[p.brand] = ensure("brands", p.brand)
		}
		if _, ok := categories[p.category]; !ok {
			categories[p.category] = ensure("categories", p.category)
		}
	}
	prefixes := []string{"Arunika", "Cakrawala", "Nusantara", "Sentosa", "Pelangi", "Bina", "Tunas", "Sinar", "Mitra", "Lestari", "Pusaka"}
	suffixes := []string{"Niaga", "Persada", "Mandiri", "Sejahtera", "Abadi", "Prima", "Utama", "Jaya", "Berkah", "Karya"}
	vendors, customers := []string{}, []string{}
	for _, a := range prefixes {
		for _, b := range suffixes {
			v := "PT " + a + " " + b + " Logistik"
			c := "PT " + a + " " + b + " Solusi"
			_, e := tx.Exec("INSERT IGNORE INTO vendors(name) VALUES(?)", v)
			must(e)
			_, e = tx.Exec("INSERT IGNORE INTO customers(name) VALUES(?)", c)
			must(e)
			vendors = append(vendors, v)
			customers = append(customers, c)
		}
	}
	type seeded struct {
		id         int64
		cost, sell float64
		unit       string
	}
	var items []seeded
	ix := 0
	for _, p := range catalog {
		for _, variant := range p.variants {
			ix++
			retail := float64(p.retail) * (0.90 + rng.Float64()*0.20)
			cost := price(retail * (0.68 + rng.Float64()*0.13))
			sell := price(retail * (0.97 + rng.Float64()*0.10))
			sku := fmt.Sprintf("IDR-2026-%03d", ix)
			name := p.name + " — " + variant
			res, e := tx.Exec("INSERT INTO items(sku,name,brand_id,category_id,unit,reorder_level) VALUES(?,?,?,?,?,?)", sku, name, brands[p.brand], categories[p.category], p.unit, 5+rng.Intn(11))
			itemID := id(res, e)
			items = append(items, seeded{itemID, cost, sell, p.unit})
		}
	}
	// Shuffle the item order so received, pending and rejected requests span categories.
	rng.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
	reasons := []string{"Replenishment", "Quarterly office supplies", "New team setup", "Replacement stock", "Warehouse restock"}
	for i, p := range items {
		created := time.Now()
		if demo {
			days := created.Day()
			if days > 14 {
				days = 14
			}
			created = created.AddDate(0, 0, -(i % days))
		}
		quantity := float64(20 + rng.Intn(61))
		if p.cost > 300000 {
			quantity = float64(5 + rng.Intn(11))
		}
		status := "approved"
		if i >= 120 && i < 135 {
			status = "pending"
		} else if i >= 135 {
			status = "rejected"
		}
		reason := reasons[rng.Intn(len(reasons))]
		res, e := tx.Exec("INSERT INTO purchase_requests(item_id,quantity,estimated_cost,reason,status,created_at) VALUES(?,?,?,?,?,?)", p.id, quantity, p.cost, reason, status, created)
		requestID := id(res, e)
		if status != "approved" {
			continue
		}
		supplier := vendors[rng.Intn(len(vendors))]
		res, e = tx.Exec("INSERT INTO purchase_orders(request_id,item_id,quantity,received,unit_cost,supplier,status,created_at) VALUES(?,?,?,?,?,?,'received',?)", requestID, p.id, quantity, quantity, p.cost, supplier, created)
		orderID := id(res, e)
		_, e = tx.Exec("UPDATE items SET quantity=?,average_cost=? WHERE id=?", quantity, p.cost, p.id)
		must(e)
		_, e = tx.Exec("INSERT INTO movements(item_id,kind,quantity,unit_cost,note,created_at) VALUES(?,'GR',?,?,?,?)", p.id, quantity, p.cost, fmt.Sprintf("Purchase order #%d", orderID), created)
		must(e)
		sold := float64(1 + rng.Intn(4))
		customer := customers[rng.Intn(len(customers))]
		res, e = tx.Exec("INSERT INTO sales(item_id,quantity,unit_price,unit_cost,customer,created_at) VALUES(?,?,?,?,?,?)", p.id, sold, p.sell, p.cost, customer, created)
		saleID := id(res, e)
		_, e = tx.Exec("UPDATE items SET quantity=quantity-? WHERE id=?", sold, p.id)
		must(e)
		_, e = tx.Exec("INSERT INTO movements(item_id,kind,quantity,unit_cost,note,sale_id,created_at) VALUES(?,'SALE',?,?,?,?,?)", p.id, -sold, p.cost, "Customer order", saleID, created)
		must(e)
		if i < 110 {
			issue := float64(1)
			_, e = tx.Exec("UPDATE items SET quantity=quantity-? WHERE id=?", issue, p.id)
			must(e)
			_, e = tx.Exec("INSERT INTO movements(item_id,kind,quantity,unit_cost,note,created_at) VALUES(?,'GI',?,?,?,?)", p.id, -issue, p.cost, "Internal office use", created)
			must(e)
		}
	}
	// Correct only the original, recognizable training chair records created by this project.
	var existing int
	must(tx.QueryRow(`SELECT COUNT(*) FROM items WHERE id=1 AND sku='CHAIR-001' AND name='Ergonomic office chair' AND quantity=15 AND average_cost=110`).Scan(&existing))
	if existing == 1 {
		var linked int
		must(tx.QueryRow(`SELECT COUNT(*) FROM purchase_orders WHERE id IN (1,2) AND item_id=1 AND ((id=1 AND unit_cost=100) OR (id=2 AND unit_cost=120))`).Scan(&linked))
		var sale int
		must(tx.QueryRow(`SELECT COUNT(*) FROM sales WHERE id=1 AND item_id=1 AND quantity=3 AND unit_price=150 AND unit_cost=110`).Scan(&sale))
		if linked == 2 && sale == 1 {
			for _, q := range []string{`UPDATE purchase_requests SET estimated_cost=estimated_cost*6500 WHERE id IN(1,2) AND item_id=1`, `UPDATE purchase_orders SET unit_cost=unit_cost*6500 WHERE id IN(1,2) AND item_id=1`, `UPDATE movements SET unit_cost=unit_cost*6500 WHERE id IN(1,2,3,4) AND item_id=1`, `UPDATE sales SET unit_cost=715000,unit_price=975000 WHERE id=1 AND item_id=1`, `UPDATE items SET average_cost=715000 WHERE id=1`} {
				_, e = tx.Exec(q)
				must(e)
			}
		}
	}
	_, e = tx.Exec("INSERT INTO app_migrations(name) VALUES('market_inventory_202611_v1')")
	must(e)
	must(tx.Commit())
	fmt.Printf("Added %d items, %d purchase requests, 120 orders and receipts, 120 sales, 110 goods issues, %d vendors and %d customers.\n", len(items), len(items), len(vendors), len(customers))
	fmt.Println("Prices are illustrative IDR estimates; all postings use the current open period.")
}
