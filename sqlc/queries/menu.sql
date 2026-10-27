-- The menus' queries: menu_categories, menus and stock_movements.
-- Every query names the outlet, so nothing reads across outlets.

-- Categories

-- name: CountCategories :one
SELECT count(*) FROM menu_categories
WHERE outlet_id = @outlet_id AND deleted_at IS NULL;

-- name: ListCategories :many
-- In the order the cashier shows them, with how many menus each holds.
SELECT c.category_id, c.name, c.icon, c.sort_order, c.is_active, c.created_at, c.updated_at,
    (SELECT count(*) FROM menus m WHERE m.category_id = c.category_id AND m.deleted_at IS NULL) AS menu_count
FROM menu_categories c
WHERE c.outlet_id = @outlet_id AND c.deleted_at IS NULL
ORDER BY c.sort_order, lower(c.name), c.category_id
LIMIT @page_size OFFSET @page_offset;

-- name: GetCategory :one
SELECT c.category_id, c.name, c.icon, c.sort_order, c.is_active, c.created_at, c.updated_at,
    (SELECT count(*) FROM menus m WHERE m.category_id = c.category_id AND m.deleted_at IS NULL) AS menu_count
FROM menu_categories c
WHERE c.category_id = @category_id AND c.outlet_id = @outlet_id AND c.deleted_at IS NULL;

-- name: LockCategory :one
-- For a change or a delete: menus being added to the category wait (they
-- take LockCategoryShared), so none slips into a category being deleted.
SELECT category_id, name, icon, is_active FROM menu_categories
WHERE category_id = @category_id AND outlet_id = @outlet_id AND deleted_at IS NULL
FOR UPDATE;

-- name: LockCategoryShared :one
-- For a menu joining the category: it can't be deleted until that commits.
SELECT category_id FROM menu_categories
WHERE category_id = @category_id AND outlet_id = @outlet_id AND deleted_at IS NULL
FOR SHARE;

-- name: NextCategoryOrder :one
-- A new category goes last.
SELECT COALESCE(max(sort_order) + 1, 0)::integer FROM menu_categories
WHERE outlet_id = @outlet_id AND deleted_at IS NULL;

-- name: InsertCategory :exec
INSERT INTO menu_categories (category_id, outlet_id, name, icon, sort_order)
VALUES (@category_id, @outlet_id, @name, @icon, @sort_order);

-- name: UpdateCategory :exec
UPDATE menu_categories SET name = @name, icon = @icon, updated_at = now()
WHERE category_id = @category_id;

-- name: SetCategoryActive :exec
UPDATE menu_categories SET is_active = @is_active, updated_at = now()
WHERE category_id = @category_id;

-- name: CountCategoryMenus :one
SELECT count(*) FROM menus
WHERE category_id = @category_id AND deleted_at IS NULL;

-- name: DeleteCategory :exec
UPDATE menu_categories SET deleted_at = now(), updated_at = now()
WHERE category_id = @category_id;

-- name: LockCategoryIDs :many
-- Every category of the outlet, locked while they are put in a new order.
SELECT category_id FROM menu_categories
WHERE outlet_id = @outlet_id AND deleted_at IS NULL
ORDER BY sort_order, lower(name), category_id
FOR UPDATE;

-- name: SetCategoryOrder :exec
-- Puts the outlet's categories in the order of category_ids, from 0.
UPDATE menu_categories c SET sort_order = o.pos - 1, updated_at = now()
FROM unnest(@category_ids::text[]) WITH ORDINALITY AS o(category_id, pos)
WHERE c.category_id = o.category_id AND c.outlet_id = @outlet_id AND c.sort_order <> o.pos - 1;

-- Menus

-- name: CountMenus :one
SELECT count(*) FROM menus m
WHERE m.outlet_id = @outlet_id AND m.deleted_at IS NULL
    AND (sqlc.narg(category_id)::text IS NULL OR m.category_id = sqlc.narg(category_id))
    AND (sqlc.narg(active)::boolean IS NULL OR m.is_active = sqlc.narg(active))
    AND (sqlc.narg(stock_status)::text IS NULL
        OR menu_stock_status(m.track_stock, m.stock_qty, m.stock_alert) = sqlc.narg(stock_status))
    AND (sqlc.narg(search)::text IS NULL
        OR m.name ILIKE '%' || sqlc.narg(search)::text || '%'
        OR m.sku ILIKE '%' || sqlc.narg(search)::text || '%');

-- name: ListMenus :many
-- In the cashier's order: by category, then name. search is already
-- escaped for LIKE.
SELECT m.menu_id, m.name, m.description, m.sku, m.price, m.image_key, m.track_stock, m.stock_qty,
    m.stock_alert, m.is_active, m.created_at, m.updated_at,
    menu_stock_status(m.track_stock, m.stock_qty, m.stock_alert)::text AS stock_status,
    c.category_id, c.name AS category_name, c.icon AS category_icon, c.is_active AS category_active
FROM menus m
JOIN menu_categories c ON c.category_id = m.category_id
WHERE m.outlet_id = @outlet_id AND m.deleted_at IS NULL
    AND (sqlc.narg(category_id)::text IS NULL OR m.category_id = sqlc.narg(category_id))
    AND (sqlc.narg(active)::boolean IS NULL OR m.is_active = sqlc.narg(active))
    AND (sqlc.narg(stock_status)::text IS NULL
        OR menu_stock_status(m.track_stock, m.stock_qty, m.stock_alert) = sqlc.narg(stock_status))
    AND (sqlc.narg(search)::text IS NULL
        OR m.name ILIKE '%' || sqlc.narg(search)::text || '%'
        OR m.sku ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY c.sort_order, lower(c.name), lower(m.name), m.menu_id
LIMIT @page_size OFFSET @page_offset;

-- name: GetMenu :one
SELECT m.menu_id, m.name, m.description, m.sku, m.price, m.image_key, m.track_stock, m.stock_qty,
    m.stock_alert, m.is_active, m.created_at, m.updated_at,
    menu_stock_status(m.track_stock, m.stock_qty, m.stock_alert)::text AS stock_status,
    c.category_id, c.name AS category_name, c.icon AS category_icon, c.is_active AS category_active
FROM menus m
JOIN menu_categories c ON c.category_id = m.category_id
WHERE m.menu_id = @menu_id AND m.outlet_id = @outlet_id AND m.deleted_at IS NULL;

-- name: LockMenu :one
-- For a change to the menu or its stock: one at a time, so no two
-- adjustments read the same count.
SELECT menu_id, category_id, name, description, sku, price, track_stock, stock_qty, stock_alert, is_active
FROM menus
WHERE menu_id = @menu_id AND outlet_id = @outlet_id AND deleted_at IS NULL
FOR UPDATE;

-- name: InsertMenu :exec
INSERT INTO menus (menu_id, outlet_id, category_id, name, description, sku, price, track_stock, stock_qty,
    stock_alert, is_active)
VALUES (@menu_id, @outlet_id, @category_id, @name, @description, @sku, @price, @track_stock, @stock_qty,
    @stock_alert, @is_active);

-- name: UpdateMenu :exec
UPDATE menus SET category_id = @category_id, name = @name, description = @description, sku = @sku,
    price = @price, track_stock = @track_stock, stock_alert = @stock_alert, updated_at = now()
WHERE menu_id = @menu_id;

-- name: SetMenuStock :exec
UPDATE menus SET stock_qty = @stock_qty, updated_at = now()
WHERE menu_id = @menu_id;

-- name: SetMenuActive :exec
UPDATE menus SET is_active = @is_active, updated_at = now()
WHERE menu_id = @menu_id;

-- name: DeleteMenu :exec
UPDATE menus SET deleted_at = now(), updated_at = now()
WHERE menu_id = @menu_id;

-- Stock

-- name: InsertStockMovement :exec
INSERT INTO stock_movements (stock_movement_id, outlet_id, menu_id, type, qty_change, stock_after, reason, note,
    user_id, order_id)
VALUES (@stock_movement_id, @outlet_id, @menu_id, @type, @qty_change, @stock_after, @reason, @note,
    @user_id, @order_id);

-- name: SetMenuStockAlert :exec
UPDATE menus SET stock_alert = @stock_alert, updated_at = now()
WHERE menu_id = @menu_id;

-- name: CountStockMovements :one
SELECT count(*)
FROM stock_movements
WHERE menu_id = @menu_id AND outlet_id = @outlet_id;

-- name: ListStockMovements :many
-- A menu's stock history, newest first: the order its count changed in,
-- since each row is stamped when it is written, under the menu's lock.
SELECT stock_movement_id, type, qty_change, stock_after, reason, note, user_id, order_id, created_at
FROM stock_movements
WHERE menu_id = @menu_id AND outlet_id = @outlet_id
ORDER BY created_at DESC, stock_movement_id DESC
LIMIT @page_size OFFSET @page_offset;

-- The cashier

-- name: ListPosCategories :many
-- The active categories, in the cashier's order. The service keeps those
-- that hold one of the cashier's menus, and counts them from that list.
SELECT category_id, name, icon
FROM menu_categories
WHERE outlet_id = @outlet_id AND deleted_at IS NULL AND is_active
ORDER BY sort_order, lower(name), category_id;

-- name: ListPosMenus :many
-- The cashier's menus: the active menus of active categories. Those it can
-- sell come first and those out of stock last; within each, by the
-- categories' order, then name.
SELECT m.menu_id, m.name, m.description, m.sku, m.price, m.image_key, m.track_stock, m.stock_qty,
    m.stock_alert, m.is_active, m.created_at, m.updated_at,
    menu_stock_status(m.track_stock, m.stock_qty, m.stock_alert)::text AS stock_status,
    c.category_id, c.name AS category_name, c.icon AS category_icon, c.is_active AS category_active
FROM menus m
JOIN menu_categories c ON c.category_id = m.category_id
WHERE m.outlet_id = @outlet_id AND m.deleted_at IS NULL AND m.is_active
    AND c.deleted_at IS NULL AND c.is_active
ORDER BY menu_stock_status(m.track_stock, m.stock_qty, m.stock_alert) = 'out',
    c.sort_order, lower(c.name), lower(m.name), m.menu_id;

-- name: LockMenusForSale :many
-- An order's menus, deleted ones left out, locked in the order of their IDs:
-- two orders of the same menus then never wait for each other in a circle.
SELECT m.menu_id, m.name, m.price, m.track_stock, m.stock_qty,
    (m.is_active AND c.is_active AND c.deleted_at IS NULL)::boolean AS available
FROM menus m
JOIN menu_categories c ON c.category_id = m.category_id
WHERE m.outlet_id = @outlet_id AND m.menu_id = ANY (@menu_ids::text[]) AND m.deleted_at IS NULL
ORDER BY m.menu_id
FOR UPDATE OF m;
