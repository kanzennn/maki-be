package models

import "slices"

// The payment methods there are. Cash is the one that works offline, so it
// stays on; QRIS and debit come with Next Dev, and stay off until then.
const (
	PaymentCash  = "cash"
	PaymentQRIS  = "qris"
	PaymentDebit = "debit"
)

// PaymentMethods are the methods there are, in the order the cashier offers
// them.
var PaymentMethods = []string{PaymentCash, PaymentQRIS, PaymentDebit}

// PaymentMethodReleased reports whether the method can be used yet.
func PaymentMethodReleased(method string) bool { return method == PaymentCash }

// KnownPaymentMethod reports whether method is one of PaymentMethods.
func KnownPaymentMethod(method string) bool { return slices.Contains(PaymentMethods, method) }

// PaymentMethod is one of an outlet's methods, and whether it is on.
type PaymentMethod struct {
	Method  string
	Enabled bool
}

// PaymentMethodResponse is a method in GET /outlets/{oid}/payment-methods.
type PaymentMethodResponse struct {
	Method    string `json:"method"`
	IsEnabled bool   `json:"is_enabled"`
	// Available is false for a method not released yet, which stays off.
	Available bool `json:"available"`
}

// PaymentMethodsResponse is an outlet's Payment Control.
type PaymentMethodsResponse struct {
	Methods []PaymentMethodResponse `json:"methods"`
}

// NewPaymentMethodsResponse returns the methods' response.
func NewPaymentMethodsResponse(methods []PaymentMethod) PaymentMethodsResponse {
	out := PaymentMethodsResponse{Methods: make([]PaymentMethodResponse, len(methods))}
	for i, m := range methods {
		out.Methods[i] = PaymentMethodResponse{Method: m.Method, IsEnabled: m.Enabled, Available: PaymentMethodReleased(m.Method)}
	}
	return out
}
