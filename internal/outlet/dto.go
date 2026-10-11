package outlet

import "time"

// outletRequest is the body of POST and PUT. The caps here only bound what
// the server reads; check applies the real rules.
type outletRequest struct {
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

// input converts the request, whose fields Input repeats without JSON tags;
// a field added to one but not the other stops this compiling.
func (r outletRequest) input() Input {
	return Input(r)
}

type statusRequest struct {
	Status string `json:"status" binding:"required,oneof=active inactive"`
}

type deleteRequest struct {
	ConfirmName string `json:"confirm_name" binding:"required,max=400"`
}

type managerResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type outletResponse struct {
	OutletID   string            `json:"outlet_id"`
	Code       string            `json:"code"`
	Name       string            `json:"name"`
	Address    *string           `json:"address"`
	Phone      *string           `json:"phone"`
	Timezone   string            `json:"timezone"`
	Locale     string            `json:"locale"`
	OpenTime   *string           `json:"open_time"`
	CloseTime  *string           `json:"close_time"`
	TaxPct     float64           `json:"tax_pct"`
	ServicePct float64           `json:"service_pct"`
	Status     string            `json:"status"`
	Managers   []managerResponse `json:"managers"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

func toResponse(d Detail) outletResponse {
	status := "inactive"
	if d.Active {
		status = "active"
	}
	managers := make([]managerResponse, len(d.Managers))
	for i, m := range d.Managers {
		managers[i] = managerResponse{UserID: m.UserID, Name: m.Name}
	}
	return outletResponse{
		OutletID:   d.OutletID,
		Code:       d.Code,
		Name:       d.Name,
		Address:    d.Address,
		Phone:      d.Phone,
		Timezone:   d.Timezone,
		Locale:     d.Locale,
		OpenTime:   d.OpenTime,
		CloseTime:  d.CloseTime,
		TaxPct:     float64(d.TaxBPS) / 100,
		ServicePct: float64(d.ServiceBPS) / 100,
		Status:     status,
		Managers:   managers,
		CreatedAt:  d.CreatedAt.UTC(),
		UpdatedAt:  d.UpdatedAt.UTC(),
	}
}
