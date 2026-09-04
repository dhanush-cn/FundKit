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
//
// Translation now includes the money unit. The domain works in integer paise;
// the .proto declares these fields as `double` and existing clients decode them
// as rupees. Rather than break that contract, the conversion happens here, at
// the edge, in exactly two functions — which is the same rule the HTTP layer
// follows and the reason the lossy step can be pointed at during review.
//
// Moving the wire format to int64 paise would be the better end state; it is a
// breaking proto change and belongs in its own versioned release.
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
		TotalValue: valuation.TotalValue.Rupees(),
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
			InvestedAmount: holding.InvestedAmount.Rupees(),
			Nav:            holding.NAV,
			CurrentValue:   holding.CurrentValue.Rupees(),
			UnrealizedGain: holding.UnrealizedGain.Rupees(),
		})
	}

	return &pb.PnLResponse{
		UserId:              valuation.UserID,
		TotalValue:          valuation.TotalValue.Rupees(),
		TotalUnrealizedGain: valuation.TotalUnrealizedGain.Rupees(),
		Holdings:            holdings,
	}
}
