// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/domain"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/service"
	"github.com/dhanush-cn/fundkit/portfolio-service/pb"
)

// PortfolioServer adapts the gRPC contract onto the service layer. It contains
// no arithmetic of its own: its whole job is translation and error mapping.
type PortfolioServer struct {
	pb.UnimplementedPortfolioServiceServer

	portfolios *service.PortfolioService
	logger     *slog.Logger
}

func NewPortfolioServer(portfolios *service.PortfolioService, logger *slog.Logger) *PortfolioServer {
	return &PortfolioServer{portfolios: portfolios, logger: logger}
}

// GetPortfolio returns current holdings and total value.
func (s *PortfolioServer) GetPortfolio(ctx context.Context, req *pb.PortfolioRequest) (*pb.PortfolioResponse, error) {
	userID := req.GetUserId()
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	valuation, err := s.portfolios.Valuate(ctx, userID)
	if err != nil {
		s.logger.ErrorContext(ctx, "portfolio valuation failed", slog.String("error", err.Error()))
		return nil, status.Error(codes.Internal, "unable to value portfolio")
	}

	holdings := make([]*pb.MutualFundHolding, 0, len(valuation.Holdings))
	for _, holding := range valuation.Holdings {
		holdings = append(holdings, &pb.MutualFundHolding{
			FundName: holding.FundName,
			Units:    holding.Units,
			Nav:      holding.NAV,
		})
	}

	return &pb.PortfolioResponse{
		UserId:     valuation.UserID,
		TotalValue: valuation.TotalValue,
		Holdings:   holdings,
	}, nil
}

// GetUserPnL returns holdings enriched with unrealized gain.
func (s *PortfolioServer) GetUserPnL(ctx context.Context, req *pb.PnLRequest) (*pb.PnLResponse, error) {
	userID := req.GetUserId()
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	valuation, err := s.portfolios.Valuate(ctx, userID)
	if err != nil {
		s.logger.ErrorContext(ctx, "pnl valuation failed", slog.String("error", err.Error()))
		return nil, status.Error(codes.Internal, "unable to compute pnl")
	}

	return toPnLResponse(valuation), nil
}

func toPnLResponse(valuation domain.PortfolioValuation) *pb.PnLResponse {
	holdings := make([]*pb.HoldingPnL, 0, len(valuation.Holdings))
	for _, holding := range valuation.Holdings {
		holdings = append(holdings, &pb.HoldingPnL{
			FundId:         holding.FundID,
			FundName:       holding.FundName,
			Units:          holding.Units,
			InvestedAmount: holding.InvestedAmount,
			Nav:            holding.NAV,
			CurrentValue:   holding.CurrentValue,
			UnrealizedGain: holding.UnrealizedGain,
		})
	}

	return &pb.PnLResponse{
		UserId:              valuation.UserID,
		TotalValue:          valuation.TotalValue,
		TotalUnrealizedGain: valuation.TotalUnrealizedGain,
		Holdings:            holdings,
	}
}
