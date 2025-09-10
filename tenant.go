
package main
import ("errors"; "fmt"; "net/http"; "strconv")
func tenantFromHeaders(r *http.Request) (int64,int64,error){
  org := r.Header.Get("X-Org-ID"); flow := r.Header.Get("X-Flow-ID")
  if org=="" || flow=="" { return 0,0, errors.New("X-Org-ID and X-Flow-ID required") }
  o, err := strconv.ParseInt(org,10,64); if err!=nil { return 0,0, fmt.Errorf("invalid X-Org-ID") }
  f, err := strconv.ParseInt(flow,10,64); if err!=nil { return 0,0, fmt.Errorf("invalid X-Flow-ID") }
  return o,f,nil
}
