
package main
import ("encoding/json"; "net/http"; "time"; "fmt"; "github.com/go-chi/chi/v5")
type Lead struct{ ID int64 `json:"id"`; OrgID int64 `json:"org_id"`; FlowID int64 `json:"flow_id"`; Name string `json:"name"`; Phone string `json:"phone"`; Stage string `json:"stage"`; CreatedAt time.Time `json:"created_at"` }
type Order struct{ ID int64 `json:"id"`; OrgID int64 `json:"org_id"`; FlowID int64 `json:"flow_id"`; LeadID int64 `json:"lead_id"`; TotalCents int `json:"total_cents"`; Status string `json:"status"`; CreatedAt time.Time `json:"created_at"` }
func (a *App) mountLeads(r chi.Router){ r.Get("/leads", a.listLeads); r.Post("/leads", a.createLead) }
func (a *App) mountOrders(r chi.Router){ r.Get("/orders", a.listOrders); r.Post("/orders", a.createOrder) }
func (a *App) mountAnalytics(r chi.Router){
  r.Get("/analytics/top-products", a.analyticsTopProducts)
  r.Get("/analytics/sales-by-hour", a.analyticsSalesByHour)
  r.Get("/analytics/summary", a.analyticsSummary)
}
func (a *App) listLeads(w http.ResponseWriter, r *http.Request){ orgID, flowID, _ := tenantFromHeaders(r); rows, err := a.DB.Query(r.Context(), `SELECT id,org_id,flow_id,name,phone,stage,created_at FROM leads WHERE org_id=$1 AND flow_id=$2 ORDER BY created_at DESC LIMIT 500`, orgID, flowID); if err != nil { http.Error(w, err.Error(), 500); return }; defer rows.Close(); var out []Lead; for rows.Next(){ var v Lead; if err := rows.Scan(&v.ID,&v.OrgID,&v.FlowID,&v.Name,&v.Phone,&v.Stage,&v.CreatedAt); err != nil { http.Error(w, err.Error(), 500); return }; out = append(out, v) }; json.NewEncoder(w).Encode(map[string]any{"items": out}) }
func (a *App) createLead(w http.ResponseWriter, r *http.Request){ var in struct{ OrgID, FlowID int64; Name, Phone, Stage string }; if err := json.NewDecoder(r.Body).Decode(&in); err != nil { http.Error(w, err.Error(), 400); return }; var id int64; var created time.Time; err := a.DB.QueryRow(r.Context(), `INSERT INTO leads(org_id,flow_id,name,phone,stage) VALUES($1,$2,$3,$4,$5) RETURNING id, created_at`, in.OrgID,in.FlowID,in.Name,in.Phone,in.Stage).Scan(&id,&created); if err != nil { http.Error(w, err.Error(), 500); return }; json.NewEncoder(w).Encode(Lead{ID:id, OrgID:in.OrgID, FlowID:in.FlowID, Name:in.Name, Phone:in.Phone, Stage:in.Stage, CreatedAt:created}) }
func (a *App) listOrders(w http.ResponseWriter, r *http.Request){ orgID, flowID, _ := tenantFromHeaders(r); rows, err := a.DB.Query(r.Context(), `SELECT id,org_id,flow_id,lead_id,total_cents,status,created_at FROM orders WHERE org_id=$1 AND flow_id=$2 ORDER BY created_at DESC LIMIT 500`, orgID, flowID); if err != nil { http.Error(w, err.Error(), 500); return }; defer rows.Close(); var out []Order; for rows.Next(){ var v Order; if err := rows.Scan(&v.ID,&v.OrgID,&v.FlowID,&v.LeadID,&v.TotalCents,&v.Status,&v.CreatedAt); err != nil { http.Error(w, err.Error(), 500); return }; out = append(out, v) }; json.NewEncoder(w).Encode(map[string]any{"items": out}) }
func (a *App) createOrder(w http.ResponseWriter, r *http.Request){ var in struct{ OrgID, FlowID int64; LeadID int64; TotalCents int; Status string }; if err := json.NewDecoder(r.Body).Decode(&in); err != nil { http.Error(w, err.Error(), 400); return }; var id int64; var created time.Time; err := a.DB.QueryRow(r.Context(), `INSERT INTO orders(org_id,flow_id,lead_id,total_cents,status) VALUES($1,$2,$3,$4,$5) RETURNING id, created_at`, in.OrgID,in.FlowID,in.LeadID,in.TotalCents,in.Status).Scan(&id,&created); if err != nil { http.Error(w, err.Error(), 500); return }; json.NewEncoder(w).Encode(Order{ID:id, OrgID:in.OrgID, FlowID:in.FlowID, LeadID:in.LeadID, TotalCents:in.TotalCents, Status:in.Status, CreatedAt:created}) }
func (a *App) analyticsTopProducts(w http.ResponseWriter, r *http.Request){
  orgID, flowID, _ := tenantFromHeaders(r)
  q := `SELECT oi.product_id, p.title, SUM(oi.qty) AS units, SUM(oi.qty*oi.unit_price_cents) AS revenue_cents FROM order_items oi JOIN products p ON p.id = oi.product_id WHERE oi.org_id=$1 AND oi.flow_id=$2 GROUP BY oi.product_id,p.title ORDER BY units DESC LIMIT 10`
  rows, err := a.DB.Query(r.Context(), q, orgID, flowID); if err != nil { http.Error(w, err.Error(), 500); return }
  defer rows.Close()
  type row struct{ ProductID int64 `json:"product_id"`; Title string `json:"title"`; Units int64 `json:"units"`; RevenueCents int64 `json:"revenue_cents"`}
  out := []row{}
  for rows.Next(){ var x row; if err:=rows.Scan(&x.ProductID,&x.Title,&x.Units,&x.RevenueCents); err!=nil { http.Error(w, err.Error(), 500); return }; out=append(out,x) }
  json.NewEncoder(w).Encode(map[string]any{"items": out})
}
func (a *App) analyticsSalesByHour(w http.ResponseWriter, r *http.Request){
  orgID, flowID, _ := tenantFromHeaders(r)
  q := `SELECT date_trunc('hour', created_at) AS t, COUNT(*) FROM orders WHERE org_id=$1 AND flow_id=$2 AND status='paid' GROUP BY 1 ORDER BY 1`
  rows, err := a.DB.Query(r.Context(), q, orgID, flowID); if err != nil { http.Error(w, err.Error(), 500); return }
  defer rows.Close()
  type row struct{ T time.Time `json:"t"`; C int64 `json:"c"` }
  out := []row{}
  for rows.Next(){ var x row; if err:=rows.Scan(&x.T,&x.C); err!=nil { http.Error(w, err.Error(), 500); return }; out=append(out,x) }
  json.NewEncoder(w).Encode(map[string]any{"items": out})
}

// analyticsSummary retorna dados agregados para a tela de análise. Ele inclui
// total de leads, total de pedidos pagos, taxa de conversão, leads
// recuperados (clientes), melhor horário de conversão (faixa de uma hora)
// baseado em pedidos pagos, e o produto mais vendido. Caso algum valor não
// possa ser calculado, campos vazios ou zero são retornados.
func (a *App) analyticsSummary(w http.ResponseWriter, r *http.Request){
  orgID, flowID, _ := tenantFromHeaders(r)
  ctx := r.Context()

  // total de leads
  var leadsCount int64
  if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM leads WHERE org_id=$1 AND flow_id=$2`, orgID, flowID).Scan(&leadsCount); err != nil {
    http.Error(w, err.Error(), 500)
    return
  }

  // total de pedidos pagos (conversões/vendas)
  var salesCount int64
  if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM orders WHERE org_id=$1 AND flow_id=$2 AND status='paid'`, orgID, flowID).Scan(&salesCount); err != nil {
    http.Error(w, err.Error(), 500)
    return
  }

  // leads recuperados (clientes)
  var recoveredCount int64
  if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM leads WHERE org_id=$1 AND flow_id=$2 AND LOWER(stage)='cliente'`, orgID, flowID).Scan(&recoveredCount); err != nil {
    http.Error(w, err.Error(), 500)
    return
  }

  // melhor horário de conversão (hora com mais pedidos pagos)
  var bestTime *time.Time
  _ = a.DB.QueryRow(ctx,
    `SELECT date_trunc('hour', created_at) AS t
     FROM orders
     WHERE org_id=$1 AND flow_id=$2 AND status='paid'
     GROUP BY 1
     ORDER BY COUNT(*) DESC
     LIMIT 1`, orgID, flowID).Scan(&bestTime)

  bestRange := ""
  if bestTime != nil {
    h := bestTime.Hour()
    next := (h + 1) % 24
    bestRange = fmt.Sprintf("%02d:00-%02d:00", h, next)
  }

  // produto mais vendido (pelo número de unidades)
  var topProduct string
  _ = a.DB.QueryRow(ctx,
    `SELECT p.title
     FROM order_items oi
     JOIN products p ON p.id = oi.product_id
     WHERE oi.org_id=$1 AND oi.flow_id=$2
     GROUP BY p.title
     ORDER BY SUM(oi.qty) DESC
     LIMIT 1`, orgID, flowID).Scan(&topProduct)

  // aproximação do total de conversas: utiliza o total de leads como proxy
  conversations := leadsCount
  var convRate float64
  if leadsCount > 0 {
    convRate = float64(salesCount) / float64(leadsCount) * 100
  } else {
    convRate = 0
  }

  out := map[string]any{
    "conversations":    conversations,
    "leads":            leadsCount,
    "sales":            salesCount,
    "conversion_rate":  convRate,
    "recovered_leads":  recoveredCount,
    "best_time_range":  bestRange,
    "top_product":      topProduct,
  }
  json.NewEncoder(w).Encode(out)
}
