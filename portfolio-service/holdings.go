package main

import "time"

type Holding struct {
	FundID         string
	FundName       string
	Units          float64
	InvestedAmount float64
}

var holdingsStore = map[string][]Holding{
	"user-1": {
		{FundID: "fund-axi-blue", FundName: "AXI Bluechip", Units: 12.5, InvestedAmount: 1500},
		{FundID: "fund-icici-growth", FundName: "ICICI Growth", Units: 8.2, InvestedAmount: 1200},
	},
	"user-2": {
		{FundID: "fund-hdfc-top", FundName: "HDFC Top 100", Units: 20.0, InvestedAmount: 3200},
	},
}

func defaultHoldings() []Holding {
	return []Holding{}
}

func navTTL() time.Duration {
	return 30 * time.Second
}
