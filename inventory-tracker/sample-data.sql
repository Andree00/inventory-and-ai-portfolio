-- Optional: run once against a fresh demo database after starting the app once.
-- Use only when the open period is the current calendar month.
START TRANSACTION;
INSERT INTO brands(name) VALUES ('Evergreen');
SET @brand_id = LAST_INSERT_ID();
INSERT INTO categories(name) VALUES ('Office supplies');
SET @category_id = LAST_INSERT_ID();
INSERT INTO items(sku,name,brand_id,category_id,unit,reorder_level) VALUES ('CHAIR-001','Ergonomic office chair',@brand_id,@category_id,'pcs',5);
SET @item_id = LAST_INSERT_ID();
INSERT INTO purchase_requests(item_id,quantity,estimated_cost,reason,status) VALUES (@item_id,10,100,'Initial stock','approved');
SET @request_id = LAST_INSERT_ID();
INSERT INTO purchase_orders(request_id,item_id,quantity,received,unit_cost,supplier,status) VALUES (@request_id,@item_id,10,10,100,'Atlas Supplies','received');
SET @order_id = LAST_INSERT_ID();
UPDATE items SET quantity=10,average_cost=100 WHERE id=@item_id;
INSERT INTO movements(item_id,kind,quantity,unit_cost,note) VALUES (@item_id,'GR',10,100,CONCAT('Purchase order #',@order_id));
INSERT INTO sales(item_id,quantity,unit_price,unit_cost,customer) VALUES (@item_id,3,150,100,'Example customer');
SET @sale_id = LAST_INSERT_ID();
UPDATE items SET quantity=7 WHERE id=@item_id;
INSERT INTO movements(item_id,kind,quantity,unit_cost,note,sale_id) VALUES (@item_id,'SALE',-3,100,'Example customer',@sale_id);
COMMIT;
