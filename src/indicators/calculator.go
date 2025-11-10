package indicators

import (
	"vagues-go/src/models"

	"github.com/markcheno/go-talib"
)

// Calculator handles technical indicator calculations
type Calculator struct {
	periods int // Number of periods to keep for calculations
}

// NewCalculator creates a new indicator calculator
func NewCalculator(periods int) *Calculator {
	return &Calculator{
		periods: periods,
	}
}

// CalculateIndicators calculates all technical indicators from K-line data
func (c *Calculator) CalculateIndicators(klines []models.KLine) []models.Indicators {
	// Need at least 30 periods for EMA30 (minimum requirement for our strategy)
	if len(klines) < 30 {
		return nil
	}

	indicators := make([]models.Indicators, len(klines))

	// Extract price data
	closes := make([]float64, len(klines))
	highs := make([]float64, len(klines))
	lows := make([]float64, len(klines))
	volumes := make([]float64, len(klines))

	for i, kline := range klines {
		closes[i] = kline.Close
		highs[i] = kline.High
		lows[i] = kline.Low
		volumes[i] = kline.Volume
	}

	// Calculate EMAs (only calculate what we have enough data for)
	ema8 := talib.Ema(closes, 8)
	ema30 := talib.Ema(closes, 30)

	var ema55, ema144, ema169 []float64
	if len(klines) >= 55 {
		ema55 = talib.Ema(closes, 55)
	}
	if len(klines) >= 144 {
		ema144 = talib.Ema(closes, 144)
	}
	if len(klines) >= 169 {
		ema169 = talib.Ema(closes, 169)
	}

	// Calculate MACD (needs at least 26 periods)
	var macd, macdSignal, macdHistogram []float64
	if len(klines) >= 26 {
		macd, macdSignal, macdHistogram = talib.Macd(closes, 12, 26, 9)
	}

	// Calculate RSI (needs at least 14 periods)
	var rsi []float64
	if len(klines) >= 14 {
		rsi = talib.Rsi(closes, 14)
	}

	// Populate indicators
	// For EMA30, we need at least 30 periods, so start from index 29
	minStartIdx := 29 // EMA30 needs 30 periods
	for i := range indicators {
		if i >= minStartIdx {
			indicators[i] = models.Indicators{
				EMA8:          getValueAt(ema8, i),
				EMA30:         getValueAt(ema30, i),
				EMA55:         getValueAt(ema55, i),
				EMA144:        getValueAt(ema144, i),
				EMA169:        getValueAt(ema169, i),
				MACD:          getValueAt(macd, i),
				MACDSignal:    getValueAt(macdSignal, i),
				MACDHistogram: getValueAt(macdHistogram, i),
				RSI:           getValueAt(rsi, i),
			}
		}
	}

	return indicators
}

// getValueAt safely gets a value from a slice, returning 0 if index is out of bounds
func getValueAt(slice []float64, index int) float64 {
	if index < 0 || index >= len(slice) {
		return 0
	}
	return slice[index]
}

// IsBullishTrend checks if the current market is in a bullish trend
func IsBullishTrend(data models.MarketData) bool {
	return data.Indicators.EMA8 > data.Indicators.EMA30 &&
		data.Indicators.EMA30 > data.Indicators.EMA55 &&
		data.Indicators.EMA55 > data.Indicators.EMA144 &&
		data.Indicators.EMA144 > data.Indicators.EMA169 &&
		data.KLine.Close > data.Indicators.EMA8 &&
		data.KLine.Close > data.Indicators.EMA30 &&
		data.Indicators.MACDHistogram > 0
}

// IsBearishTrend checks if the current market is in a bearish trend
func IsBearishTrend(data models.MarketData) bool {
	return data.Indicators.EMA8 < data.Indicators.EMA30 &&
		data.Indicators.EMA30 < data.Indicators.EMA55 &&
		data.Indicators.EMA55 < data.Indicators.EMA144 &&
		data.Indicators.EMA144 < data.Indicators.EMA169 &&
		data.KLine.Close < data.Indicators.EMA8 &&
		data.KLine.Close < data.Indicators.EMA30 &&
		data.Indicators.MACDHistogram < 0
}

// GetTrendDirection returns the current trend direction
func GetTrendDirection(data models.MarketData) models.TrendDirection {
	if IsBullishTrend(data) {
		return models.TrendBullish
	} else if IsBearishTrend(data) {
		return models.TrendBearish
	}
	return models.TrendNeutral
}
