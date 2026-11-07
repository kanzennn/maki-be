package services

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
)

// DashboardService adds up the outlets' sales for the Owner's dashboard and
// each outlet's own. A period is dates, and each outlet's days are its own,
// in its time zone. Sales are the paid orders, by when the device made them;
// voids are counted apart.
type DashboardService struct {
	orders  *repositories.OrderRepository
	menus   *repositories.MenuRepository
	outlets *repositories.OutletRepository
	users   *UserService
	tables  *TableService
}

// NewDashboardService returns the dashboards' service.
func NewDashboardService(orders *repositories.OrderRepository, menus *repositories.MenuRepository, outlets *repositories.OutletRepository,
	users *UserService, tables *TableService,
) *DashboardService {
	return &DashboardService{orders: orders, menus: menus, outlets: outlets, users: users, tables: tables}
}

// Owner returns the dashboard of the query's outlets, or of every outlet.
func (s *DashboardService) Owner(ctx context.Context, q models.DashboardQuery) (models.DashboardResponse, error) {
	zones, err := s.outlets.Zones(ctx, q.OutletIDs)
	if err != nil {
		return models.DashboardResponse{}, err
	}
	d, current, previous, err := s.common(ctx, q, zones)
	if err != nil {
		return models.DashboardResponse{}, err
	}
	sold, err := s.orders.SalesByOutlet(ctx, current)
	if err != nil {
		return models.DashboardResponse{}, err
	}
	before, err := s.orders.SalesByOutlet(ctx, previous)
	if err != nil {
		return models.DashboardResponse{}, err
	}
	for _, z := range zones {
		line := models.DashboardOutletResponse{OutletID: z.OutletID, Code: z.Code, Name: z.Name}
		if i := slices.IndexFunc(sold, func(o models.OutletSales) bool { return o.OutletID == z.OutletID }); i >= 0 {
			line.Sales, line.Transactions = sold[i].Sales, sold[i].Transactions
		}
		if i := slices.IndexFunc(before, func(o models.OutletSales) bool { return o.OutletID == z.OutletID }); i >= 0 {
			line.PreviousSales, line.PreviousTransactions = before[i].Sales, before[i].Transactions
		}
		d.Outlets = append(d.Outlets, line)
	}
	slices.SortStableFunc(d.Outlets, func(a, b models.DashboardOutletResponse) int { return cmp.Compare(b.Sales, a.Sales) })
	return d, nil
}

// Outlet returns the outlet's dashboard, with its hours, its cashiers and the
// tables customers are seated at now.
func (s *DashboardService) Outlet(ctx context.Context, outletID string, q models.DashboardQuery) (models.DashboardResponse, error) {
	zones, err := s.outlets.Zones(ctx, []string{outletID})
	if err != nil {
		return models.DashboardResponse{}, err
	}
	d, current, _, err := s.common(ctx, q, zones)
	if err != nil {
		return models.DashboardResponse{}, err
	}
	byHour, err := s.orders.SalesByHour(ctx, current)
	if err != nil {
		return models.DashboardResponse{}, err
	}
	for hour := range 24 {
		line := models.DashboardHourResponse{Hour: hour}
		if i := slices.IndexFunc(byHour, func(h models.DaySales) bool { return h.Hour == hour }); i >= 0 {
			line.Sales, line.Transactions = byHour[i].Sales, byHour[i].Transactions
		}
		d.Hours = append(d.Hours, line)
	}
	cashiers, err := s.orders.SalesByCashier(ctx, current)
	if err != nil {
		return models.DashboardResponse{}, err
	}
	ids := make([]string, len(cashiers))
	for i, c := range cashiers {
		ids[i] = c.UserID
	}
	names, err := s.users.Names(ctx, ids)
	if err != nil {
		return models.DashboardResponse{}, err
	}
	for _, c := range cashiers {
		d.Cashiers = append(d.Cashiers, models.DashboardCashierResponse{
			UserID: c.UserID, Name: names[c.UserID], Sales: c.Sales, Transactions: c.Transactions,
		})
	}
	floor, err := s.tables.Floor(ctx, outletID)
	if err != nil {
		return models.DashboardResponse{}, err
	}
	for _, area := range floor.Areas {
		for _, t := range area.Tables {
			if t.Status != models.TableOccupied {
				continue
			}
			line := models.DashboardTableResponse{TableID: t.TableID, Name: t.Name, AreaName: area.Name}
			if t.Seated != nil {
				line.Orders = t.Seated.Orders
			}
			d.Tables = append(d.Tables, line)
		}
	}
	return d, nil
}

// common adds up what both dashboards show, and returns the periods it used.
func (s *DashboardService) common(ctx context.Context, q models.DashboardQuery, zones []models.OutletZone) (d models.DashboardResponse, current, previous models.SalesPeriod, err error) {
	from, err := time.Parse(time.DateOnly, q.From)
	if err != nil {
		return d, current, previous, fmt.Errorf("dashboard from: %w", err)
	}
	to, err := time.Parse(time.DateOnly, q.To)
	if err != nil {
		return d, current, previous, fmt.Errorf("dashboard to: %w", err)
	}
	days := int(to.Sub(from).Hours()/24) + 1
	prevTo := from.AddDate(0, 0, -1)
	prevFrom := prevTo.AddDate(0, 0, -(days - 1))

	if current, err = salesPeriod(zones, from, to); err != nil {
		return d, current, previous, err
	}
	if previous, err = salesPeriod(zones, prevFrom, prevTo); err != nil {
		return d, current, previous, err
	}
	d = models.DashboardResponse{
		From: q.From, To: q.To, PreviousFrom: prevFrom.Format(time.DateOnly), PreviousTo: prevTo.Format(time.DateOnly),
		Days: []models.DashboardDayResponse{}, Hours: []models.DashboardHourResponse{}, Outlets: []models.DashboardOutletResponse{},
		PaymentMethods: []models.DashboardMethodResponse{}, TopMenus: []models.DashboardMenuResponse{},
		LowStock: []models.DashboardLowStockResponse{}, Cashiers: []models.DashboardCashierResponse{}, Tables: []models.DashboardTableResponse{},
	}

	sold, err := s.orders.SalesByOutlet(ctx, current)
	if err != nil {
		return d, current, previous, err
	}
	for _, o := range sold {
		d.Summary.Sales += o.Sales
		d.Summary.Transactions += o.Transactions
		d.Summary.VoidTotal += o.VoidTotal
		d.Summary.VoidCount += o.VoidCount
	}
	if d.Summary.Transactions > 0 {
		d.Summary.AverageTransaction = d.Summary.Sales / int64(d.Summary.Transactions)
	}
	before, err := s.orders.SalesByOutlet(ctx, previous)
	if err != nil {
		return d, current, previous, err
	}
	for _, o := range before {
		d.Summary.PreviousSales += o.Sales
		d.Summary.PreviousTransactions += o.Transactions
	}
	if d.Summary.ItemsSold, err = s.orders.ItemsSold(ctx, current); err != nil {
		return d, current, previous, err
	}

	byDay, err := s.orders.SalesByDay(ctx, current)
	if err != nil {
		return d, current, previous, err
	}
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		line := models.DashboardDayResponse{Date: day.Format(time.DateOnly)}
		if i := slices.IndexFunc(byDay, func(x models.DaySales) bool { return x.Day == line.Date }); i >= 0 {
			line.Sales, line.Transactions = byDay[i].Sales, byDay[i].Transactions
		}
		d.Days = append(d.Days, line)
	}

	methods, err := s.orders.SalesByMethod(ctx, current)
	if err != nil {
		return d, current, previous, err
	}
	for _, m := range methods {
		d.PaymentMethods = append(d.PaymentMethods, models.DashboardMethodResponse(m))
	}
	top, err := s.orders.TopMenus(ctx, current)
	if err != nil {
		return d, current, previous, err
	}
	for _, m := range top {
		d.TopMenus = append(d.TopMenus, models.DashboardMenuResponse(m))
	}

	names := make(map[string]string, len(zones))
	for _, z := range zones {
		names[z.OutletID] = z.Name
	}
	low, err := s.menus.LowStock(ctx, current.OutletIDs)
	if err != nil {
		return d, current, previous, err
	}
	for _, m := range low {
		d.LowStock = append(d.LowStock, models.DashboardLowStockResponse{
			MenuID: m.MenuID, OutletID: m.OutletID, OutletName: names[m.OutletID], Name: m.Name,
			StockQty: m.StockQty, StockAlert: m.StockAlert, StockStatus: m.StockStatus,
		})
	}
	return d, current, previous, nil
}

// salesPeriod works the dates out, from and to included, into the instants
// each outlet's days start and end at, in its time zone.
func salesPeriod(zones []models.OutletZone, from, to time.Time) (models.SalesPeriod, error) {
	var p models.SalesPeriod
	for _, z := range zones {
		loc, err := time.LoadLocation(z.Timezone)
		if err != nil {
			return p, fmt.Errorf("load time zone %q: %w", z.Timezone, err)
		}
		p.OutletIDs = append(p.OutletIDs, z.OutletID)
		p.Timezones = append(p.Timezones, z.Timezone)
		p.Starts = append(p.Starts, time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc))
		p.Ends = append(p.Ends, time.Date(to.Year(), to.Month(), to.Day()+1, 0, 0, 0, 0, loc))
	}
	return p, nil
}
