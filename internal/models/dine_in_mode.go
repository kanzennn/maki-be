package models

// DineInMode is how an outlet seats its dine-in customers. One at a time:
// the Manager picks it, and it holds from the next sale on.
type DineInMode string

const (
	// ModeTable seats them at the tables of the outlet's layout, which the
	// cashier may pick. An outlet that never picked a mode has it.
	ModeTable DineInMode = "table"
	// ModeNumber seats them wherever they set down the number stand the
	// cashier gave them, where a waiter brings the order: a dine-in sale
	// carries the stand's number, and no table.
	ModeNumber DineInMode = "number"
)

// Valid reports whether m is one of the modes.
func (m DineInMode) Valid() bool { return m == ModeTable || m == ModeNumber }

// DineInModeResponse is the outlet's dine-in mode, as GET and PUT
// …/dine-in-mode send it.
type DineInModeResponse struct {
	Mode string `json:"mode"`
}
