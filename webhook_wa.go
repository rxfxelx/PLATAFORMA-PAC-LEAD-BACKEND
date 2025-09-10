package main

import "net/http"

// webhookWa lida com eventos de webhook enviados pela uazapi para uma instância
// específica. Neste momento, o handler apenas confirma a recepção da
// requisição com status 202 (Accepted), permitindo que a API de terceiros
// descarte a mensagem. Uma implementação futura pode encaminhar o corpo
// recebido para o agente de IA responsável pela instância.
func (a *App) webhookWa(w http.ResponseWriter, r *http.Request) {
    // TODO: encaminhar eventos para o agente IA correspondente ao "instance".
    w.WriteHeader(http.StatusAccepted)
    _, _ = w.Write([]byte("queued"))
}