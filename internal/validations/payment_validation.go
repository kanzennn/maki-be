package validations

import (
	"strconv"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// PaymentMethodsRequest is the body of PUT /outlets/{oid}/payment-methods:
// the methods to set, the others left as they are.
type PaymentMethodsRequest struct {
	Methods []PaymentMethodRequest `json:"methods" binding:"required,max=10"`
}

// PaymentMethodRequest is a method to set.
type PaymentMethodRequest struct {
	Method    string `json:"method"`
	IsEnabled *bool  `json:"is_enabled"`
}

// CheckPaymentMethods validates the methods to set: at least one, each known,
// once, and saying whether it is on.
func CheckPaymentMethods(r PaymentMethodsRequest) ([]models.PaymentMethod, error) {
	if len(r.Methods) == 0 {
		return nil, apperror.Invalid(map[string]string{"methods": "required"})
	}
	fields := map[string]string{}
	seen := map[string]bool{}
	out := make([]models.PaymentMethod, 0, len(r.Methods))
	for i, m := range r.Methods {
		at := "methods." + strconv.Itoa(i) + "."
		switch {
		case m.Method == "":
			fields[at+"method"] = "required"
		case !models.KnownPaymentMethod(m.Method):
			fields[at+"method"] = "oneof"
		case seen[m.Method]:
			fields[at+"method"] = "duplicate"
		}
		seen[m.Method] = true
		if m.IsEnabled == nil {
			fields[at+"is_enabled"] = "required"
			continue
		}
		out = append(out, models.PaymentMethod{Method: m.Method, Enabled: *m.IsEnabled})
	}
	if len(fields) > 0 {
		return nil, apperror.Invalid(fields)
	}
	return out, nil
}
