package models

// The payment methods there are. Cash is the one that works offline, so it
// stays on; QRIS and debit come with Next Dev, and stay off until then.
const (
	PaymentCash  = "cash"
	PaymentQRIS  = "qris"
	PaymentDebit = "debit"
)
