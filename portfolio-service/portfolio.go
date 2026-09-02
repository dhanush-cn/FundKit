package main

import (
	"context"
	"math"
	"time"

	"portfolio-service/cache"
	"portfolio-service/pb"
)

type PortfolioServer struct {
	pb.UnimplementedPortfolioServiceServer
}

func NewPortfolioServer() *PortfolioServer {
	return &PortfolioServer{}
}

func (s *PortfolioServer) GetPortfolio(ctx context.Context, req *pb.PortfolioRequest) (*pb.PortfolioResponse, error) {
	userID := req.GetUserId()
	holdings := holdingsStore[userID]
	if holdings == nil {
		holdings = defaultHoldings()
	}

	var totalValue float64
	responseHoldings := make([]*pb.MutualFundHolding, 0, len(holdings))
	for _, holding := range holdings {
		nav := s.getNavPrice(holding.FundID)
		currentValue := nav * holding.Units
		totalValue += currentValue
		responseHoldings = append(responseHoldings, &pb.MutualFundHolding{
			FundName: holding.FundName,
			Units:    holding.Units,
			Nav:      nav,
		})
	}

	return &pb.PortfolioResponse{
		UserId:     userID,
		TotalValue: round2(totalValue),
		Holdings:   responseHoldings,
	}, nil
}

func (s *PortfolioServer) GetUserPnL(ctx context.Context, req *pb.PnLRequest) (*pb.PnLResponse, error) {
	userID := req.GetUserId()
	holdings := holdingsStore[userID]
	if holdings == nil {
		holdings = defaultHoldings()
	}

	var totalValue float64
	var totalUnrealized float64
	responseHoldings := make([]*pb.HoldingPnL, 0, len(holdings))
	for _, holding := range holdings {
		nav := s.getNavPrice(holding.FundID)
		currentValue := nav * holding.Units
		unrealizedGain := currentValue - holding.InvestedAmount

		totalValue += currentValue
		totalUnrealized += unrealizedGain

		responseHoldings = append(responseHoldings, &pb.HoldingPnL{
			FundId:         holding.FundID,
			FundName:       holding.FundName,
			Units:          holding.Units,
			InvestedAmount: holding.InvestedAmount,
			Nav:            nav,
			CurrentValue:   round2(currentValue),
			UnrealizedGain: round2(unrealizedGain),
		})
	}

	return &pb.PnLResponse{
		UserId:              userID,
		TotalValue:          round2(totalValue),
		TotalUnrealizedGain: round2(totalUnrealized),
		Holdings:            responseHoldings,
	}, nil
}

func (s *PortfolioServer) getNavPrice(fundID string) float64 {
	price, ok, err := cache.GetNavPrice(fundID)
	if err == nil && ok {
		return price
	}

	fresh := fetchNavPrice(fundID)
	_ = cache.SetNavPrice(fundID, fresh, navTTL())
	return fresh
}

func fetchNavPrice(fundID string) float64 {
	base := map[string]float64{
		"fund-axi-blue":     125.45,
		"fund-icici-growth": 98.20,
		"fund-hdfc-top":     160.10,
	}

	seed := base[fundID]
	if seed == 0 {
		seed = 100.00
	}

	jitter := float64(time.Now().Unix()%7) * 0.12
	return round2(seed + jitter)
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}
