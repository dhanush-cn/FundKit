// Engineered by Dhanush C N (github.com/dhanush-cn)
package repository

import (
	"context"
	"time"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/domain"
)

// NAVSource simulates the upstream AMC feed that publishes daily net asset
// values. It is the slow, rate-limited dependency the Redis cache exists to
// protect.
type NAVSource struct {
	base map[string]float64
}

func NewNAVSource() *NAVSource {
	return &NAVSource{
		base: map[string]float64{
			"fund-axi-blue":     125.45,
			"fund-icici-growth": 98.20,
			"fund-hdfc-top":     160.10,
		},
	}
}

// Fetch returns the latest NAV for a fund.
func (s *NAVSource) Fetch(_ context.Context, fundID string) (float64, error) {
	seed, ok := s.base[fundID]
	if !ok {
		seed = 100.00
	}
	jitter := float64(time.Now().Unix()%7) * 0.12
	return domain.RoundNAV(seed + jitter), nil
}
