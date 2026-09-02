package main

import (
	"log"
	"net"
	"net/http"
	"os"

	"google.golang.org/grpc"

	"portfolio-service/cache"
	"portfolio-service/pb"
)

func main() {
	log.Println("Portfolio Service Starting...")
	cache.InitRedis()
	go startHTTPServer()

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterPortfolioServiceServer(s, NewPortfolioServer())

	log.Println("Portfolio gRPC server listening on :50051")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func startHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"UP","service":"portfolio-service"}`))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Printf("portfolio HTTP server stopped: %v", err)
	}
}
