package validations

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// VoucherRequest is the body of POST /vouchers and PUT /vouchers/{vid}. The
// caps only bound what the server reads; CheckVoucher applies the real
// rules.
type VoucherRequest struct {
	Code string  `json:"code" binding:"required,max=64"`
	Name *string `json:"name" binding:"omitempty,max=400"`
	Type string  `json:"type" binding:"required,max=16"`
	// Value is basis points for a percentage (1000 is 10%), rupiah for an
	// amount.
	Value       *int64     `json:"value" binding:"required"`
	MaxDiscount *int64     `json:"max_discount"`
	MinSpend    *int64     `json:"min_spend"`
	Quota       *int       `json:"quota" binding:"required"`
	StartAt     *time.Time `json:"start_at"`
	ExpiredAt   *time.Time `json:"expired_at" binding:"required"`
	AllOutlets  *bool      `json:"all_outlets" binding:"required"`
	OutletIDs   []string   `json:"outlet_ids" binding:"max=1000,dive,max=64"`
	// IsActive is for a new voucher, active when left out; an existing one
	// changes it with PATCH …/status.
	IsActive *bool `json:"is_active"`
}

// Input converts the request.
func (r VoucherRequest) Input() models.VoucherInput {
	return models.VoucherInput{
		Code: r.Code, Name: r.Name, Type: r.Type, Value: r.Value, MaxDiscount: r.MaxDiscount, MinSpend: r.MinSpend,
		Quota: r.Quota, StartAt: r.StartAt, ExpiredAt: r.ExpiredAt, AllOutlets: r.AllOutlets != nil && *r.AllOutlets,
		OutletIDs: r.OutletIDs, Active: r.IsActive,
	}
}

// CheckVoucherRequest is the body of POST /outlets/{oid}/vouchers/validate:
// the code the customer gave, and the cart's subtotal.
type CheckVoucherRequest struct {
	Code     string `json:"code" binding:"required,max=64"`
	Subtotal *int64 `json:"subtotal" binding:"required"`
}

// CheckVoucherFilter checks a voucher list's filters: a status is one of a
// voucher's. The search comes back trimmed.
func CheckVoucherFilter(f models.VoucherFilter) (models.VoucherFilter, error) {
	switch f.Status {
	case "", models.VoucherActive, models.VoucherScheduled, models.VoucherUsedUp, models.VoucherExpired, models.VoucherInactive:
		f.Search = strings.TrimSpace(f.Search)
		return f, nil
	}
	return models.VoucherFilter{}, apperror.Invalid(map[string]string{"status": "oneof"})
}

// NormalizeVoucherCode is a code as the vouchers keep it: capitals, without
// surrounding spaces. The Owner and the cashier may type it in any case.
func NormalizeVoucherCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// CheckVoucherLookup validates what the cashier checks a voucher with: a
// code, and a subtotal from 0 to MaxAmount. It returns the code normalized.
func CheckVoucherLookup(code string, subtotal *int64) (string, error) {
	code = NormalizeVoucherCode(code)
	problems := map[string]string{}
	if code == "" {
		problems["code"] = "required"
	}
	switch {
	case subtotal == nil:
		problems["subtotal"] = "required"
	case *subtotal < 0 || *subtotal > models.MaxAmount:
		problems["subtotal"] = "range"
	}
	if len(problems) > 0 {
		return "", apperror.Invalid(problems)
	}
	return code, nil
}

// voucherCodePattern is the vouchers_code_check constraint's rule.
var voucherCodePattern = regexp.MustCompile(`^[A-Z0-9]{3,20}$`)

// CheckVoucher validates a voucher as filled in, and returns its problems
// by field: every one at once, for validation_failed, to which the voucher
// service adds those it checks against the stored voucher. Only a
// percentage has a maximum discount, and only a voucher for some outlets
// has outlets; the others' are dropped. Times are kept to the second.
func CheckVoucher(in models.VoucherInput) (models.VoucherFields, map[string]string) {
	problems := map[string]string{}
	v := models.VoucherFields{
		Code: NormalizeVoucherCode(in.Code), Name: trimmed(in.Name), VoucherTerms: models.VoucherTerms{Type: in.Type},
		AllOutlets: in.AllOutlets,
	}
	switch {
	case v.Code == "":
		problems["code"] = "required"
	case !voucherCodePattern.MatchString(v.Code):
		problems["code"] = "format"
	}
	if v.Name != nil && utf8.RuneCountInString(*v.Name) > models.MaxVoucherName {
		problems["name"] = "max"
	}
	maxValue := int64(models.MaxAmount)
	switch in.Type {
	case models.VoucherPercent:
		maxValue = models.MaxPercentBPS
	case models.VoucherAmount:
	case "":
		problems["type"] = "required"
	default:
		problems["type"] = "oneof"
	}
	switch {
	case in.Value == nil:
		problems["value"] = "required"
	case *in.Value < 1 || *in.Value > maxValue:
		problems["value"] = "range"
	default:
		v.Value = *in.Value
	}
	if in.Type == models.VoucherPercent {
		v.MaxDiscount = amount(in.MaxDiscount, problems, "max_discount")
	}
	v.MinSpend = amount(in.MinSpend, problems, "min_spend")
	switch {
	case in.Quota == nil:
		problems["quota"] = "required"
	case *in.Quota < 1 || *in.Quota > models.MaxQuota:
		problems["quota"] = "range"
	default:
		v.Quota = *in.Quota
	}
	if in.StartAt != nil {
		v.StartAt = toSecond(in.StartAt)
	}
	if in.ExpiredAt == nil {
		problems["expired_at"] = "required"
	} else {
		v.ExpiredAt = *toSecond(in.ExpiredAt)
		if v.StartAt != nil && !v.ExpiredAt.After(*v.StartAt) {
			problems["expired_at"] = "after"
		}
	}
	if !v.AllOutlets {
		v.OutletIDs = slices.Compact(slices.Sorted(slices.Values(in.OutletIDs)))
		if len(v.OutletIDs) == 0 {
			problems["outlet_ids"] = "required"
		}
	}
	return v, problems
}

// amount checks an optional amount of rupiah, from 1 to MaxAmount.
func amount(v *int64, problems map[string]string, field string) *int64 {
	switch {
	case v == nil:
		return nil
	case *v < 1 || *v > models.MaxAmount:
		problems[field] = "range"
		return nil
	}
	return v
}

// toSecond is the time in UTC, to the second: a voucher's times are kept no
// finer.
func toSecond(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC().Truncate(time.Second)
	return &u
}
