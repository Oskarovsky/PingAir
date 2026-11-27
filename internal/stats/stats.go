package stats

import "github.com/Oskarovsky/PingAir/internal/provider"

type RouteStats struct {
	Samples int
	Mean    float64
	Min     float64
	Max     float64
	Latest  float64
}

type Breakout struct {
	Quote          provider.Quote
	Stats          RouteStats
	DropPctVsMean  float64
}

func Summarize(prices []float64, latest float64) *RouteStats {
	if len(prices) == 0 {
		return nil
	}
	sum := 0.0
	min, max := prices[0], prices[0]
	for _, p := range prices {
		sum += p
		if p < min {
			min = p
		}
		if p > max {
			max = p
		}
	}
	return &RouteStats{
		Samples: len(prices),
		Mean:    sum / float64(len(prices)),
		Min:     min,
		Max:     max,
		Latest:  latest,
	}
}

func DetectBreakout(quote provider.Quote, pricesIncludingLatest []float64, dropPct float64, minSamples int) *Breakout {
	if len(pricesIncludingLatest) < minSamples {
		return nil
	}
	hist := pricesIncludingLatest[:len(pricesIncludingLatest)-1]
	need := minSamples - 1
	if need < 1 {
		need = 1
	}
	if len(hist) < need {
		return nil
	}
	st := Summarize(hist, quote.Price)
	if st == nil || st.Mean <= 0 {
		return nil
	}
	threshold := st.Mean * (1.0 - dropPct/100.0)
	if quote.Price > threshold {
		return nil
	}
	drop := (st.Mean - quote.Price) / st.Mean * 100.0
	return &Breakout{Quote: quote, Stats: *st, DropPctVsMean: drop}
}
