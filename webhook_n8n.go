
package main
import "net/http"
func (a *App) webhookN8N(w http.ResponseWriter, r *http.Request){ w.WriteHeader(202); w.Write([]byte("queued")) }
