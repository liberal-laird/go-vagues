package strategy

import (
	"vagues-go/src/indicators"
	"vagues-go/src/models"
)

// EMA_MACD_Strategy implements the EMA + MACD strategy
type EMA_MACD_Strategy struct {
	calculator *indicators.Calculator
	history    []models.MarketData
}

// NewEMA_MACD_Strategy creates a new strategy instance
func NewEMA_MACD_Strategy() *EMA_MACD_Strategy {
	return &EMA_MACD_Strategy{
		calculator: indicators.NewCalculator(200), // Keep 200 periods for calculations
		history:    make([]models.MarketData, 0),
	}
}

// Analyze analyzes the market data and returns trading signals
func (s *EMA_MACD_Strategy) Analyze(data models.MarketData) models.SignalType {
	// Add new data to history
	s.history = append(s.history, data)
	if len(s.history) > 200 {
		s.history = s.history[1:]
	}

	// Need at least 2 periods to detect crossovers
	if len(s.history) < 2 {
		return models.SignalNone
	}

	current := s.history[len(s.history)-1]
	previous := s.history[len(s.history)-2]

	trend := indicators.GetTrendDirection(current)

	// Check for entry signals based on trend
	switch trend {
	case models.TrendBullish:
		if s.isLongEntrySignal(current, previous) {
			return models.SignalLongEntry
		}
	case models.TrendBearish:
		if s.isShortEntrySignal(current, previous) {
			return models.SignalShortEntry
		}
	}

	// Check for exit signals
	if s.isExitSignal(current, previous) {
		if trend == models.TrendBullish {
			return models.SignalLongExit
		} else if trend == models.TrendBearish {
			return models.SignalShortExit
		}
	}

	return models.SignalNone
}

// isLongEntrySignal checks for long entry conditions
func (s *EMA_MACD_Strategy) isLongEntrySignal(current, previous models.MarketData) bool {
	// EMA8 crosses above EMA30
	ema8Cross := current.Indicators.EMA8 > current.Indicators.EMA30 &&
		previous.Indicators.EMA8 <= previous.Indicators.EMA30

	// MACD fast line crosses above slow line
	macdCross := current.Indicators.MACD > current.Indicators.MACDSignal &&
		previous.Indicators.MACD <= previous.Indicators.MACDSignal

	// Price above EMA8
	priceAboveEMA8 := current.KLine.Close > current.Indicators.EMA8

	// RSI filter (optional)
	rsiValid := current.Indicators.RSI > 50

	return ema8Cross && macdCross && priceAboveEMA8 && rsiValid
}

// isShortEntrySignal checks for short entry conditions
func (s *EMA_MACD_Strategy) isShortEntrySignal(current, previous models.MarketData) bool {
	// EMA8 crosses below EMA30
	ema8Cross := current.Indicators.EMA8 < current.Indicators.EMA30 &&
		previous.Indicators.EMA8 >= previous.Indicators.EMA30

	// MACD fast line crosses below slow line
	macdCross := current.Indicators.MACD < current.Indicators.MACDSignal &&
		previous.Indicators.MACD >= previous.Indicators.MACDSignal

	// Price below EMA8
	priceBelowEMA8 := current.KLine.Close < current.Indicators.EMA8

	// RSI filter (optional)
	rsiValid := current.Indicators.RSI < 50

	return ema8Cross && macdCross && priceBelowEMA8 && rsiValid
}

// isExitSignal checks for exit conditions
func (s *EMA_MACD_Strategy) isExitSignal(current, previous models.MarketData) bool {
	// Price crosses EMA30 (stop loss)
	priceCrossEMA30 := (current.KLine.Close < current.Indicators.EMA30 &&
		previous.KLine.Close >= previous.Indicators.EMA30) ||
		(current.KLine.Close > current.Indicators.EMA30 &&
			previous.KLine.Close <= previous.Indicators.EMA30)

	// MACD histogram starts decreasing (profit taking)
	macdHistogramDecreasing := current.Indicators.MACDHistogram < previous.Indicators.MACDHistogram

	// Price deviation from EMA8 > 1.5% (profit taking)
	priceDeviation := abs((current.KLine.Close-current.Indicators.EMA8)/current.Indicators.EMA8) > 0.015

	return priceCrossEMA30 || macdHistogramDecreasing || priceDeviation
}

// abs returns the absolute value of a float64
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// GetCurrentTrend returns the current market trend
func (s *EMA_MACD_Strategy) GetCurrentTrend() models.TrendDirection {
	if len(s.history) == 0 {
		return models.TrendNeutral
	}
	return indicators.GetTrendDirection(s.history[len(s.history)-1])
}