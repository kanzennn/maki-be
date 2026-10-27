package models

// Person is someone a record names: who activated or revoked a device, who
// took or voided an order.
type Person struct {
	UserID string
	Name   string
}

// PersonResponse is a Person as the endpoints send them.
type PersonResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

// NewPersonResponse returns the person's response.
func NewPersonResponse(p Person) PersonResponse {
	return PersonResponse(p)
}
