package main

import (
	"fmt"
	"net/http"
	"time"
)

const PORT = 3001

func main() {
	http.HandleFunc("/", handler)
	fmt.Printf("Server running on: http://localhost:%v/\n", PORT)

	addr := fmt.Sprintf(":%d", PORT)
	server := &http.Server{
		Addr:         addr,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Printf("Could not start server: %v\n", err)
		return
	}

}

func handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("hello"))
}