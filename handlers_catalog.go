package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// Product represents an item for sale. In addition to the original fields,
// it now includes optional image data encoded as base64, price in cents,
// available stock and a category. When returned via JSON, zero values are
// omitted for image, category, price and stock.
// Product represents an item for sale. In addition to the original fields,
// it now includes an image URL. The ImageBase64 field is kept internal
// (not exported via JSON) but continues to map to the existing
// image_base64 column in the database. The ImageURL field mirrors its
// contents and is returned to clients instead of the base64 data.
type Product struct {
    ID        int64     `json:"id"`
    OrgID     int64     `json:"org_id"`
    FlowID    int64     `json:"flow_id"`
    Title     string    `json:"title"`
    Slug      string    `json:"slug,omitempty"`
    Status    string    `json:"status"`
    ImageBase64 string  `json:"-"`
    ImageURL  string    `json:"image_url,omitempty"`
    PriceCents int      `json:"price_cents,omitempty"`
    Stock     int      `json:"stock,omitempty"`
    Category  string   `json:"category,omitempty"`
    CreatedAt time.Time `json:"created_at"`
}

func (a *App) mountCatalog(r chi.Router) {
	r.Get("/products", a.listProducts)
	r.Post("/products", a.createProduct)
	r.Put("/products/{id}", a.updateProduct)
	r.Delete("/products/{id}", a.deleteProduct)
}

func (a *App) listProducts(w http.ResponseWriter, r *http.Request) {
	orgID, flowID, _ := tenantFromHeaders(r)
    rows, err := a.DB.Query(r.Context(),
        `SELECT id,org_id,flow_id,title,slug,status,image_base64,price_cents,stock,category,created_at
         FROM products
         WHERE org_id=$1 AND flow_id=$2
         ORDER BY created_at DESC LIMIT 500`,
        orgID, flowID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

    var out []Product
    for rows.Next() {
        var p Product
        if err := rows.Scan(&p.ID, &p.OrgID, &p.FlowID, &p.Title, &p.Slug, &p.Status, &p.ImageBase64, &p.PriceCents, &p.Stock, &p.Category, &p.CreatedAt); err != nil {
            http.Error(w, err.Error(), 500)
            return
        }
        // Expose image URL instead of the raw base64 contents. The
        // ImageBase64 column may already contain a URL (for newer entries) or
        // a plain base64 string (legacy). In either case we forward the value
        // via the ImageURL field and omit the base64 data from the JSON.
        p.ImageURL = p.ImageBase64
        // Clear ImageBase64 so it is not marshaled (json:"-")
        p.ImageBase64 = ""
        out = append(out, p)
    }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"items": out})
}

func (a *App) createProduct(w http.ResponseWriter, r *http.Request) {
    // Accept both image_url and image_base64 fields. The legacy field
    // image_base64 is retained for backwards compatibility, but new
    // clients should send image_url containing the URL of the uploaded
    // image. If image_url is set, it is stored in the image_base64
    // column on the database (so no schema change is needed).
    var in struct {
        OrgID       int64  `json:"org_id"`
        FlowID      int64  `json:"flow_id"`
        Title       string `json:"title"`
        Slug        string `json:"slug"`
        Status      string `json:"status"`
        ImageURL    string `json:"image_url"`
        ImageBase64 string `json:"image_base64"`
        PriceCents  int    `json:"price_cents"`
        Stock       int    `json:"stock"`
        Category    string `json:"category"`
    }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid json: "+err.Error(), 400)
		return
	}

	// fallback para headers se não vier no body
	if in.OrgID == 0 || in.FlowID == 0 {
		orgID, flowID, err := tenantFromHeaders(r)
		if err == nil {
			in.OrgID, in.FlowID = orgID, flowID
		}
	}
    if in.Title == "" {
		http.Error(w, "title required", 400)
		return
	}
    if in.Status == "" {
        in.Status = "active"
    }

    // If image_url is provided, use it as the value for image_base64 so
    // that we can reuse the existing image_base64 column without schema changes.
    if in.ImageBase64 == "" && in.ImageURL != "" {
        in.ImageBase64 = in.ImageURL
    }

    // insert product with optional fields. image_base64, price_cents, stock and category
    var id int64
    var created time.Time
    err := a.DB.QueryRow(r.Context(),
        `INSERT INTO products(org_id,flow_id,title,slug,status,image_base64,price_cents,stock,category)
         VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
         RETURNING id,created_at`,
        in.OrgID, in.FlowID, in.Title, in.Slug, in.Status, in.ImageBase64, in.PriceCents, in.Stock, in.Category).Scan(&id, &created)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	p := Product{
		ID:        id,
		OrgID:     in.OrgID,
		FlowID:    in.FlowID,
		Title:     in.Title,
		Slug:      in.Slug,
		Status:    in.Status,
		CreatedAt: created,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

func (a *App) updateProduct(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
    var in struct {
        Title       string `json:"title"`
        Slug        string `json:"slug"`
        Status      string `json:"status"`
        ImageURL    string `json:"image_url"`
        ImageBase64 string `json:"image_base64"`
        PriceCents  *int   `json:"price_cents"`
        Stock       *int   `json:"stock"`
        Category    string `json:"category"`
    }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid json: "+err.Error(), 400)
		return
	}
    // If the caller sends image_url but not image_base64, use it for
    // image_base64 to preserve backwards compatibility with the existing
    // column. When both are provided, image_base64 takes precedence.
    if in.ImageBase64 == "" && in.ImageURL != "" {
        in.ImageBase64 = in.ImageURL
    }
    // Use COALESCE to update only provided fields. If price_cents or stock are
    // nil, pass NULL so COALESCE retains the existing value.
    query := `UPDATE products
      SET title=COALESCE(NULLIF($1,''),title),
          slug=COALESCE(NULLIF($2,''),slug),
          status=COALESCE(NULLIF($3,''),status),
          image_base64=COALESCE(NULLIF($4,''),image_base64),
          price_cents=COALESCE($5, price_cents),
          stock=COALESCE($6, stock),
          category=COALESCE(NULLIF($7,''),category)
      WHERE id=$8`
    var priceArg any
    if in.PriceCents != nil {
        priceArg = *in.PriceCents
    } else {
        priceArg = nil
    }
    var stockArg any
    if in.Stock != nil {
        stockArg = *in.Stock
    } else {
        stockArg = nil
    }
    _, err := a.DB.Exec(r.Context(), query,
        in.Title, in.Slug, in.Status, in.ImageBase64,
        priceArg, stockArg, in.Category, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}

func (a *App) deleteProduct(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_, err := a.DB.Exec(r.Context(), `DELETE FROM products WHERE id=$1`, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}
