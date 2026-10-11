-- name: InsertDefaultMethods :exec
-- A new outlet's methods: cash on, the others off.
INSERT INTO outlet_payment_methods (outlet_id, method, is_enabled)
VALUES (@outlet_id, 'cash', true), (@outlet_id, 'qris', false), (@outlet_id, 'debit', false);

-- name: ListEnabledMethods :many
-- In the order the cashier offers them.
SELECT method
FROM outlet_payment_methods
WHERE outlet_id = @outlet_id AND is_enabled
ORDER BY array_position(ARRAY['cash', 'qris', 'debit'], method);

-- name: MethodEnabled :one
SELECT EXISTS (
    SELECT 1 FROM outlet_payment_methods WHERE outlet_id = @outlet_id AND method = @method AND is_enabled
);
