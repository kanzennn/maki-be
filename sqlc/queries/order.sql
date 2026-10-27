-- name: FindOrder :one
-- An order by its ID alone, whichever outlet's: the ID is the device's
-- idempotency key, so a resend is told apart from another order that
-- happened to send the same ID.
SELECT outlet_id, request_hash
FROM orders
WHERE order_id = @order_id;

-- name: InsertOrder :exec
INSERT INTO orders (order_id, outlet_id, device_id, order_no, type, table_id, table_name, stand_number, cashier_id,
    subtotal, voucher_id, voucher_code, voucher_discount,
    service_bps, service, tax_bps, tax, total, client_created_at, request_hash)
VALUES (@order_id, @outlet_id, @device_id, @order_no, @type, sqlc.narg(table_id), sqlc.narg(table_name),
    sqlc.narg(stand_number), @cashier_id, @subtotal, sqlc.narg(voucher_id), sqlc.narg(voucher_code), @voucher_discount,
    @service_bps, @service, @tax_bps, @tax, @total, @client_created_at, @request_hash);

-- name: InsertOrderItems :exec
-- Every line at once, zipped from the arrays, which are all as long. An
-- empty note is none.
INSERT INTO order_items (order_item_id, outlet_id, order_id, position, menu_id, menu_name, price, qty, note,
    line_total)
SELECT unnest(@order_item_ids::text[]), @outlet_id, @order_id, unnest(@positions::int[]),
    unnest(@menu_ids::text[]), unnest(@menu_names::text[]), unnest(@prices::bigint[]), unnest(@qtys::int[]),
    NULLIF(unnest(@notes::text[]), ''), unnest(@line_totals::bigint[]);

-- name: InsertPayment :exec
INSERT INTO payments (payment_id, outlet_id, order_id, method, amount, received, change, status)
VALUES (@payment_id, @outlet_id, @order_id, @method, @amount, @received, @change, 'paid');

-- name: GetOrder :one
SELECT o.order_id, o.device_id, o.order_no, o.type, o.table_id, o.table_name, o.stand_number, o.cashier_id,
    o.subtotal, o.voucher_code, o.voucher_discount,
    o.service_bps, o.service, o.tax_bps, o.tax, o.total, o.status, o.client_created_at, o.created_at,
    o.voided_at, o.voided_by, o.void_reason,
    p.method, p.amount, p.received, p.change, p.status AS payment_status
FROM orders o
JOIN payments p ON p.order_id = o.order_id AND p.outlet_id = o.outlet_id
WHERE o.order_id = @order_id AND o.outlet_id = @outlet_id;

-- name: ListOrderItems :many
SELECT order_item_id, menu_id, menu_name, price, qty, note, line_total
FROM order_items
WHERE order_id = @order_id AND outlet_id = @outlet_id
ORDER BY position;

-- name: CountOrders :one
SELECT count(*)
FROM orders
WHERE outlet_id = @outlet_id AND client_created_at >= @day_start AND client_created_at < @day_end
    AND (sqlc.narg(search)::text IS NULL OR order_no ILIKE '%' || sqlc.narg(search)::text || '%');

-- name: ListOrders :many
-- A day's orders, newest first. search is already escaped for LIKE.
SELECT o.order_id, o.device_id, o.order_no, o.type, o.table_name, o.stand_number, o.cashier_id, o.total,
    o.status, o.client_created_at, o.created_at, p.method,
    (SELECT sum(i.qty) FROM order_items i WHERE i.order_id = o.order_id AND i.outlet_id = o.outlet_id)::int
        AS item_count
FROM orders o
JOIN payments p ON p.order_id = o.order_id AND p.outlet_id = o.outlet_id
WHERE o.outlet_id = @outlet_id AND o.client_created_at >= @day_start AND o.client_created_at < @day_end
    AND (sqlc.narg(search)::text IS NULL OR o.order_no ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY o.client_created_at DESC, o.order_id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: LastOrderNumber :one
-- The highest number of the device's count on a day: its orders made then,
-- whose receipt numbers carry that day.
SELECT COALESCE(max(split_part(order_no, '-', 4)::int), 0)::int AS last_number
FROM orders
WHERE outlet_id = @outlet_id AND device_id = @device_id
    AND client_created_at >= @day_start AND client_created_at < @day_end;

-- Void

-- name: LockOrder :one
SELECT order_no, status, total, voucher_id
FROM orders
WHERE order_id = @order_id AND outlet_id = @outlet_id
FOR UPDATE;

-- name: SoldByOrder :many
-- How many of each menu the order sold, for the void to put back.
SELECT menu_id, sum(qty)::int AS qty
FROM order_items
WHERE order_id = @order_id AND outlet_id = @outlet_id
GROUP BY menu_id
ORDER BY menu_id;

-- name: VoidOrder :exec
UPDATE orders SET status = 'void', voided_at = now(), voided_by = @voided_by, void_reason = @void_reason
WHERE order_id = @order_id;

-- name: VoidPayment :exec
UPDATE payments SET status = 'void'
WHERE order_id = @order_id AND outlet_id = @outlet_id;

-- For the other services

-- name: OutletHasOrders :one
SELECT EXISTS (SELECT 1 FROM orders WHERE outlet_id = @outlet_id);

-- name: ListTableSales :many
-- The paid dine-in sales at the outlet's tables written after since, for
-- the tables: who is seated where.
SELECT table_id::text AS table_id, created_at FROM orders
WHERE outlet_id = @outlet_id AND table_id IS NOT NULL AND type = 'dine_in' AND status = 'paid'
    AND created_at > @since;

-- name: ListVoucherUses :many
-- A voucher's sales, newest first, voided ones included.
SELECT order_id, outlet_id, order_no, client_created_at, voucher_discount, status
FROM orders
WHERE voucher_id = @voucher_id
ORDER BY created_at DESC, order_id
LIMIT @page_size OFFSET @page_offset;

-- name: CountVoucherUses :one
SELECT count(*)
FROM orders
WHERE voucher_id = @voucher_id;
