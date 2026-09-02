package main

import (
	"context"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"order-service/pb"
)

var (
	portfolioConn   *grpc.ClientConn
	portfolioClient pb.PortfolioServiceClient
)

func InitPortfolioClient() {
	address := os.Getenv("PORTFOLIO_GRPC_URL")
	if address == "" {
		address = "localhost:50051"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		log.Printf("Portfolio gRPC unavailable at %s: %v", address, err)
		return
	}

	portfolioConn = conn
	portfolioClient = pb.NewPortfolioServiceClient(conn)
	log.Printf("Portfolio gRPC client connected to %s", address)
}

func FetchUserPnL(ctx context.Context, userID string) (*pb.PnLResponse, error) {
	if portfolioClient == nil {
		return nil, ErrPortfolioUnavailable
	}

	req := &pb.PnLRequest{UserId: userID}
	return portfolioClient.GetUserPnL(ctx, req)
}
