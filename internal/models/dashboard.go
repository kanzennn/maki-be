package models

import "time"

// MaxDashboardDays is the longest period a dashboard adds up.
const MaxDashboardDays = 366

// DashboardQuery is the period a dashboard covers, as dates in each outlet's
// own time zone, from and to included, and the outlets it adds up.
type DashboardQuery struct {
	From, To string // YYYY-MM-DD
	// OutletIDs narrows the Owner's dashboard; empty is every outlet.
	OutletIDs []string
}

// OutletZone is an outlet a dashboard adds up, with its time zone.
type OutletZone struct {
	OutletID, Code, Name, Timezone string
}

// SalesPeriod is each outlet's period as the instants it starts and ends at,
// for the queries that add its orders up.
type SalesPeriod struct {
	OutletIDs, Timezones []string
	Starts, Ends         []time.Time
}

// OutletSales is what an outlet sold in a period.
type OutletSales struct {
	OutletID     string
	Sales        int64
	Transactions int
	VoidTotal    int64
	VoidCount    int
}

// DaySales is what was sold on a day, or in an hour of the day.
type DaySales struct {
	Day          string
	Hour         int
	Sales        int64
	Transactions int
}

// MethodSales is what a payment method took.
type MethodSales struct {
	Method       string
	Amount       int64
	Transactions int
}

// MenuSales is a top menu.
type MenuSales struct {
	MenuID, OutletID, Name string
	Qty, Sales             int64
}

// CashierSales is what a cashier sold.
type CashierSales struct {
	UserID       string
	Name         string
	Sales        int64
	Transactions int
}

// LowStockMenu is a menu at or below its stock alert, or out of stock.
type LowStockMenu struct {
	MenuID, OutletID, Name string
	StockQty               int
	StockAlert             *int
	StockStatus            string
}

// DashboardSummaryResponse is a dashboard's cards. Sales are the paid orders;
// voids are apart. The previous figures are the period just before, as long.
type DashboardSummaryResponse struct {
	Sales                int64 `json:"sales"`
	Transactions         int   `json:"transactions"`
	AverageTransaction   int64 `json:"average_transaction"`
	ItemsSold            int64 `json:"items_sold"`
	VoidCount            int   `json:"void_count"`
	VoidTotal            int64 `json:"void_total"`
	PreviousSales        int64 `json:"previous_sales"`
	PreviousTransactions int   `json:"previous_transactions"`
}

// DashboardDayResponse is a day of a dashboard's period, those without a sale
// included.
type DashboardDayResponse struct {
	Date         string `json:"date"`
	Sales        int64  `json:"sales"`
	Transactions int    `json:"transactions"`
}

// DashboardHourResponse is an hour of the day, 0 to 23, all of them there.
type DashboardHourResponse struct {
	Hour         int   `json:"hour"`
	Sales        int64 `json:"sales"`
	Transactions int   `json:"transactions"`
}

// DashboardOutletResponse is an outlet's line in the Owner's ranking, the
// best first.
type DashboardOutletResponse struct {
	OutletID             string `json:"outlet_id"`
	Code                 string `json:"code"`
	Name                 string `json:"name"`
	Sales                int64  `json:"sales"`
	Transactions         int    `json:"transactions"`
	PreviousSales        int64  `json:"previous_sales"`
	PreviousTransactions int    `json:"previous_transactions"`
}

// DashboardMethodResponse is what a payment method took.
type DashboardMethodResponse struct {
	Method       string `json:"method"`
	Amount       int64  `json:"amount"`
	Transactions int    `json:"transactions"`
}

// DashboardMenuResponse is a top menu.
type DashboardMenuResponse struct {
	MenuID   string `json:"menu_id"`
	OutletID string `json:"outlet_id"`
	Name     string `json:"name"`
	Qty      int64  `json:"qty"`
	Sales    int64  `json:"sales"`
}

// DashboardLowStockResponse is a menu at or below its alert, or out of stock.
type DashboardLowStockResponse struct {
	MenuID      string `json:"menu_id"`
	OutletID    string `json:"outlet_id"`
	OutletName  string `json:"outlet_name"`
	Name        string `json:"name"`
	StockQty    int    `json:"stock_qty"`
	StockAlert  *int   `json:"stock_alert"`
	StockStatus string `json:"stock_status"`
}

// DashboardCashierResponse is what a cashier sold.
type DashboardCashierResponse struct {
	UserID       string `json:"user_id"`
	Name         string `json:"name"`
	Sales        int64  `json:"sales"`
	Transactions int    `json:"transactions"`
}

// DashboardTableResponse is a table customers are seated at now.
type DashboardTableResponse struct {
	TableID  string `json:"table_id"`
	Name     string `json:"name"`
	AreaName string `json:"area_name"`
	Orders   int    `json:"orders"`
}

// DashboardResponse is GET /owner/dashboard and GET /outlets/{oid}/dashboard.
// The Owner's fills outlets; an outlet's fills hours, cashiers and tables.
type DashboardResponse struct {
	From           string                      `json:"from"`
	To             string                      `json:"to"`
	PreviousFrom   string                      `json:"previous_from"`
	PreviousTo     string                      `json:"previous_to"`
	Summary        DashboardSummaryResponse    `json:"summary"`
	Days           []DashboardDayResponse      `json:"days"`
	Hours          []DashboardHourResponse     `json:"hours"`
	Outlets        []DashboardOutletResponse   `json:"outlets"`
	PaymentMethods []DashboardMethodResponse   `json:"payment_methods"`
	TopMenus       []DashboardMenuResponse     `json:"top_menus"`
	LowStock       []DashboardLowStockResponse `json:"low_stock"`
	Cashiers       []DashboardCashierResponse  `json:"cashiers"`
	Tables         []DashboardTableResponse    `json:"tables"`
}
