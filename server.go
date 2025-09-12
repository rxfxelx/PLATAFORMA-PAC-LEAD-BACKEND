package main

import (
	"net/http"
	"strings"
)

// headerTrim retorna o header "k" já com TrimSpace (útil se precisar em outros pontos).
func headerTrim(r *http.Request, k string) string {
	return strings.TrimSpace(r.Header.Get(k))
}
