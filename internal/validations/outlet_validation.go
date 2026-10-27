package validations

import (
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/i18n"
)

// OutletRequest is the body of POST /outlets and PUT /outlets/{oid}. The
// caps only bound what the server reads; CheckOutlet applies the real
// rules.
type OutletRequest struct {
	Code       string  `json:"code" binding:"required,max=32"`
	Name       string  `json:"name" binding:"required,max=400"`
	Address    *string `json:"address" binding:"omitempty,max=2000"`
	Phone      *string `json:"phone" binding:"omitempty,max=128"`
	Timezone   string  `json:"timezone" binding:"max=64"`
	Locale     string  `json:"locale" binding:"max=16"`
	OpenTime   *string `json:"open_time" binding:"omitempty,max=16"`
	CloseTime  *string `json:"close_time" binding:"omitempty,max=16"`
	TaxPct     float64 `json:"tax_pct"`
	ServicePct float64 `json:"service_pct"`
}

// Input converts the request, whose fields models.OutletInput repeats
// without JSON tags; a field added to one but not the other stops this
// compiling.
func (r OutletRequest) Input() models.OutletInput {
	return models.OutletInput(r)
}

// OutletStatusRequest is the body of PATCH /outlets/{oid}/status.
type OutletStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=active inactive"`
}

// DeleteOutletRequest is the body of DELETE /outlets/{oid}: the outlet's
// name, typed again.
type DeleteOutletRequest struct {
	ConfirmName string `json:"confirm_name" binding:"required,max=400"`
}

// CheckOutletFilter checks the outlet list's filters: a status is active or
// inactive. The search comes back trimmed.
func CheckOutletFilter(f models.OutletFilter) (models.OutletFilter, error) {
	if f.Status != "" && f.Status != "active" && f.Status != "inactive" {
		return models.OutletFilter{}, apperror.Invalid(map[string]string{"status": "oneof"})
	}
	f.Search = strings.TrimSpace(f.Search)
	return f, nil
}

var (
	outletCodePattern = regexp.MustCompile(`^[A-Z0-9]{2,10}$`)
	hourPattern       = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

// CheckOutlet validates what the Owner filled in, every problem at once as
// validation_failed, and returns the outlet's fields: an active outlet's,
// the code in upper case, the rates in basis points.
func CheckOutlet(in models.OutletInput) (models.OutletFields, error) {
	fields := map[string]string{}
	f := models.OutletFields{
		Code:     strings.ToUpper(strings.TrimSpace(in.Code)),
		Name:     strings.TrimSpace(in.Name),
		Address:  trimmed(in.Address),
		Phone:    trimmed(in.Phone),
		Timezone: strings.TrimSpace(in.Timezone),
		Locale:   strings.TrimSpace(in.Locale),
		Status:   "active",
	}
	if !outletCodePattern.MatchString(f.Code) {
		fields["code"] = "format"
	}
	switch n := utf8.RuneCountInString(f.Name); {
	case n == 0:
		fields["name"] = "required"
	case n > 100:
		fields["name"] = "max"
	}
	if f.Address != nil && utf8.RuneCountInString(*f.Address) > 500 {
		fields["address"] = "max"
	}
	if f.Phone != nil && utf8.RuneCountInString(*f.Phone) > 32 {
		fields["phone"] = "max"
	}
	if f.Timezone == "" {
		f.Timezone = models.DefaultTimezone
	}
	if !validTimezone(f.Timezone) {
		fields["timezone"] = "timezone"
	}
	if f.Locale == "" {
		f.Locale = i18n.Default
	}
	if !i18n.Supported(f.Locale) {
		fields["locale"] = "oneof"
	}
	checkHours(in.OpenTime, in.CloseTime, &f, fields)
	f.TaxBPS = basisPoints(in.TaxPct, fields, "tax_pct")
	f.ServiceBPS = basisPoints(in.ServicePct, fields, "service_pct")
	if len(fields) > 0 {
		return models.OutletFields{}, apperror.Invalid(fields)
	}
	return f, nil
}

// validTimezone accepts an IANA zone such as Asia/Jakarta. "Local" would be
// the server's zone, not the outlet's.
func validTimezone(tz string) bool {
	if tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// checkHours sets the opening hours on f: both or neither, as "15:04", and
// never the same time twice.
func checkHours(open, closing *string, f *models.OutletFields, fields map[string]string) {
	o, c := trimmed(open), trimmed(closing)
	switch {
	case o == nil && c == nil:
		return
	case o == nil:
		fields["open_time"] = "required"
		return
	case c == nil:
		fields["close_time"] = "required"
		return
	}
	if !hourPattern.MatchString(*o) {
		fields["open_time"] = "format"
	}
	if !hourPattern.MatchString(*c) {
		fields["close_time"] = "format"
	}
	if fields["open_time"] != "" || fields["close_time"] != "" {
		return
	}
	if *o == *c {
		fields["close_time"] = "different"
		return
	}
	f.OpenTime, f.CloseTime = o, c
}

// basisPoints turns a percentage with at most two decimals, 0 to 100, into
// basis points. Anything else is noted in fields.
func basisPoints(pct float64, fields map[string]string, field string) int {
	if math.IsNaN(pct) || pct < 0 || pct > 100 {
		fields[field] = "range"
		return 0
	}
	bps := math.Round(pct * 100)
	if math.Abs(pct*100-bps) > 1e-6 {
		fields[field] = "decimals"
		return 0
	}
	return int(bps)
}
