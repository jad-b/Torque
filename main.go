package main

import (
	"fmt"
	"github.com/jad-b/crank/api"
	"log"
	"net/http"
)

// IdentityHandler echoes the hostname back to the client
func IdentityHandler(w http.ResponseWriter, req *http.Request) {
	log.Printf("host is %s", req.Host)
	fmt.Fprintf(w, "%s, this is me.", req.Host)
}

func main() {
	log.Print("Starting server")
	http.HandleFunc("/host/", IdentityHandler)
	http.HandleFunc("/workout/", api.GetWorkoutHandler)
	http.ListenAndServe(":8000", nil)
	defer log.Fatal("Stopping server")
}
