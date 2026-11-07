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

-- The dashboards. Each outlet's period is its own days, in its own time zone:
-- the caller works them out into the instants they start and end at, which
-- orders_outlet_created_idx finds. Sales are the paid orders; voids are
-- counted apart.

-- name: DashboardByOutlet :many
WITH period AS (
    SELECT unnest(@outlet_ids::text[]) AS outlet_id, unnest(@timezones::text[]) AS timezone,
        unnest(@starts::timestamptz[]) AS start_at, unnest(@ends::timestamptz[]) AS end_at
)
SELECT o.outlet_id,
    COALESCE(sum(o.total) FILTER (WHERE o.status = 'paid'), 0)::bigint AS sales,
    count(*) FILTER (WHERE o.status = 'paid')::int AS transactions,
    COALESCE(sum(o.total) FILTER (WHERE o.status = 'void'), 0)::bigint AS void_total,
    count(*) FILTER (WHERE o.status = 'void')::int AS void_count
FROM orders o
JOIN period r ON r.outlet_id = o.outlet_id
    AND o.client_created_at >= r.start_at AND o.client_created_at < r.end_at
GROUP BY o.outlet_id;

-- name: DashboardItemsSold :one
WITH period AS (
    SELECT unnest(@outlet_ids::text[]) AS outlet_id, unnest(@timezones::text[]) AS timezone,
        unnest(@starts::timestamptz[]) AS start_at, unnest(@ends::timestamptz[]) AS end_at
)
SELECT COALESCE(sum(i.qty), 0)::bigint
FROM orders o
JOIN period r ON r.outlet_id = o.outlet_id
    AND o.client_created_at >= r.start_at AND o.client_created_at < r.end_at
JOIN order_items i ON i.order_id = o.order_id AND i.outlet_id = o.outlet_id
WHERE o.status = 'paid';

-- name: DashboardByDay :many
WITH period AS (
    SELECT unnest(@outlet_ids::text[]) AS outlet_id, unnest(@timezones::text[]) AS timezone,
        unnest(@starts::timestamptz[]) AS start_at, unnest(@ends::timestamptz[]) AS end_at
)
SELECT to_char(o.client_created_at AT TIME ZONE r.timezone, 'YYYY-MM-DD')::text AS day,
    COALESCE(sum(o.total), 0)::bigint AS sales, count(*)::int AS transactions
FROM orders o
JOIN period r ON r.outlet_id = o.outlet_id
    AND o.client_created_at >= r.start_at AND o.client_created_at < r.end_at
WHERE o.status = 'paid'
GROUP BY day
ORDER BY day;

-- name: DashboardByHour :many
WITH period AS (
    SELECT unnest(@outlet_ids::text[]) AS outlet_id, unnest(@timezones::text[]) AS timezone,
        unnest(@starts::timestamptz[]) AS start_at, unnest(@ends::timestamptz[]) AS end_at
)
SELECT extract(hour FROM o.client_created_at AT TIME ZONE r.timezone)::int AS hour,
    COALESCE(sum(o.total), 0)::bigint AS sales, count(*)::int AS transactions
FROM orders o
JOIN period r ON r.outlet_id = o.outlet_id
    AND o.client_created_at >= r.start_at AND o.client_created_at < r.end_at
WHERE o.status = 'paid'
GROUP BY hour
ORDER BY hour;

-- name: DashboardByMethod :many
WITH period AS (
    SELECT unnest(@outlet_ids::text[]) AS outlet_id, unnest(@timezones::text[]) AS timezone,
        unnest(@starts::timestamptz[]) AS start_at, unnest(@ends::timestamptz[]) AS end_at
)
SELECT p.method, COALESCE(sum(p.amount), 0)::bigint AS amount, count(*)::int AS transactions
FROM orders o
JOIN period r ON r.outlet_id = o.outlet_id
    AND o.client_created_at >= r.start_at AND o.client_created_at < r.end_at
JOIN payments p ON p.order_id = o.order_id AND p.outlet_id = o.outlet_id
WHERE o.status = 'paid'
GROUP BY p.method
ORDER BY amount DESC, p.method;

-- name: DashboardTopMenus :many
-- The ten that sold the most. A menu keeps the name of its latest sale.
WITH period AS (
    SELECT unnest(@outlet_ids::text[]) AS outlet_id, unnest(@timezones::text[]) AS timezone,
        unnest(@starts::timestamptz[]) AS start_at, unnest(@ends::timestamptz[]) AS end_at
)
SELECT i.menu_id, o.outlet_id, (array_agg(i.menu_name ORDER BY o.client_created_at DESC))[1]::text AS name,
    sum(i.qty)::bigint AS qty, sum(i.line_total)::bigint AS sales
FROM orders o
JOIN period r ON r.outlet_id = o.outlet_id
    AND o.client_created_at >= r.start_at AND o.client_created_at < r.end_at
JOIN order_items i ON i.order_id = o.order_id AND i.outlet_id = o.outlet_id
WHERE o.status = 'paid'
GROUP BY i.menu_id, o.outlet_id
ORDER BY qty DESC, sales DESC, i.menu_id
LIMIT 10;

-- name: DashboardByCashier :many
WITH period AS (
    SELECT unnest(@outlet_ids::text[]) AS outlet_id, unnest(@timezones::text[]) AS timezone,
        unnest(@starts::timestamptz[]) AS start_at, unnest(@ends::timestamptz[]) AS end_at
)
SELECT o.cashier_id, COALESCE(sum(o.total), 0)::bigint AS sales, count(*)::int AS transactions
FROM orders o
JOIN period r ON r.outlet_id = o.outlet_id
    AND o.client_created_at >= r.start_at AND o.client_created_at < r.end_at
WHERE o.status = 'paid'
GROUP BY o.cashier_id
ORDER BY sales DESC, o.cashier_id;
