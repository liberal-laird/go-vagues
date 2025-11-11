package strategy

import (
	"fmt"
	"log"
	"math"
	"vagues-go/src/models"
)

// PatternVolumeDeltaStrategy implements the 1M Pattern + Volume + Delta strategy
type PatternVolumeDeltaStrategy struct {
	// Pattern detection parameters
	hRatio    float64 // Hammer ratio (default 2.0)
	bLookback int     // Breakout lookback periods (default 5)
	mRatio    float64 // Momentum candle ratio (default 0.7)

	// Volume parameters
	vLookback int     // Volume lookback window (default 20)
	vMult     float64 // Volume multiplier (default 1.25)

	// Delta parameters
	deltaLookbackTicks int     // Delta lookback ticks (default 40)
	deltaThreshMode    string  // "dynamic" or "absolute"
	deltaThreshAbs     float64 // Absolute delta threshold
	deltaDynMult       float64 // Dynamic threshold multiplier (default 0.8)

	// Trend filter
	useTrendFilter bool // Whether to use trend filter
	emaLong        int  // Long EMA period (default 30)

	// History
	history      []models.MarketData
	deltaHistory []float64 // History of delta values for dynamic threshold

	// Logging control
	verboseLogging bool // Whether to output verbose filter logs (default false)

	// Filter failure reason (for debugging)
	lastFilterFailure string // Last filter that failed
}

// NewPatternVolumeDeltaStrategy creates a new strategy instance
func NewPatternVolumeDeltaStrategy() *PatternVolumeDeltaStrategy {
	return &PatternVolumeDeltaStrategy{
		hRatio:             2.0,
		bLookback:          5,
		mRatio:             0.7,
		vLookback:          20,
		vMult:              1.25,
		deltaLookbackTicks: 40,
		deltaThreshMode:    "dynamic",
		deltaThreshAbs:     100.0,
		deltaDynMult:       0.8,
		useTrendFilter:     true,
		emaLong:            30,
		history:            make([]models.MarketData, 0),
		deltaHistory:       make([]float64, 0),
		verboseLogging:     false, // 默认关闭详细日志
	}
}

// SetVerboseLogging 设置是否输出详细日志
func (s *PatternVolumeDeltaStrategy) SetVerboseLogging(enabled bool) {
	s.verboseLogging = enabled
}

// GetLastFilterFailure 获取最后一次过滤失败的原因
func (s *PatternVolumeDeltaStrategy) GetLastFilterFailure() string {
	return s.lastFilterFailure
}

// Analyze analyzes the market data and returns trading signals
func (s *PatternVolumeDeltaStrategy) Analyze(data models.MarketData, delta models.Delta) models.SignalType {
	// Add new data to history
	s.history = append(s.history, data)
	if len(s.history) > 200 {
		s.history = s.history[1:]
	}

	// Add delta to history
	s.deltaHistory = append(s.deltaHistory, delta.Value)
	if len(s.deltaHistory) > 100 {
		s.deltaHistory = s.deltaHistory[1:]
	}

	// Need at least 2 periods for pattern detection
	if len(s.history) < 2 {
		return models.SignalNone
	}

	current := s.history[len(s.history)-1]
	previous := s.history[len(s.history)-2]

	// 1. Pattern detection
	pattern := s.detectPatterns(current, previous)
	if pattern.Direction == models.SignalNone {
		s.lastFilterFailure = "Pattern检测未通过"
		if s.verboseLogging {
			log.Printf("策略过滤: Pattern检测未通过")
		}
		return models.SignalNone
	}
	if s.verboseLogging {
		log.Printf("策略过滤: Pattern检测通过 - %s (方向: %s, 置信度: %.2f)", pattern.Name, getPatternDirectionName(pattern.Direction), pattern.Confidence)
	}

	// 2. Volume filter
	volOk := s.checkVolume(current)
	if !volOk {
		s.lastFilterFailure = fmt.Sprintf("Volume过滤未通过 (当前成交量: %.2f)", current.KLine.Volume)
		if s.verboseLogging {
			log.Printf("策略过滤: Volume过滤未通过 (当前成交量: %.2f)", current.KLine.Volume)
		}
		return models.SignalNone
	}
	if s.verboseLogging {
		log.Printf("策略过滤: Volume过滤通过 (当前成交量: %.2f)", current.KLine.Volume)
	}

	// 3. Delta filter
	deltaOk := s.checkDelta(delta, pattern.Direction)
	if !deltaOk {
		s.lastFilterFailure = fmt.Sprintf("Delta过滤未通过 (Delta值: %.2f, 方向: %s)", delta.Value, getPatternDirectionName(pattern.Direction))
		if s.verboseLogging {
			log.Printf("策略过滤: Delta过滤未通过 (Delta值: %.2f, 方向: %s)", delta.Value, getPatternDirectionName(pattern.Direction))
		}
		return models.SignalNone
	}
	if s.verboseLogging {
		log.Printf("策略过滤: Delta过滤通过 (Delta值: %.2f)", delta.Value)
	}

	// 4. Trend filter (optional)
	if s.useTrendFilter {
		trendOk := s.checkTrend(current, pattern.Direction)
		if !trendOk {
			s.lastFilterFailure = fmt.Sprintf("Trend过滤未通过 (价格: %.4f, EMA30: %.4f, 方向: %s)", current.KLine.Close, current.Indicators.EMA30, getPatternDirectionName(pattern.Direction))
			if s.verboseLogging {
				log.Printf("策略过滤: Trend过滤未通过 (价格: %.4f, EMA30: %.4f, 方向: %s)", current.KLine.Close, current.Indicators.EMA30, getPatternDirectionName(pattern.Direction))
			}
			return models.SignalNone
		}
		if s.verboseLogging {
			log.Printf("策略过滤: Trend过滤通过 (价格: %.4f, EMA30: %.4f)", current.KLine.Close, current.Indicators.EMA30)
		}
	}

	// All filters passed, return entry signal
	s.lastFilterFailure = "" // 清除失败原因
	if s.verboseLogging {
		log.Printf("策略过滤: ✅ 所有过滤条件通过，生成 %s 信号", getPatternDirectionName(pattern.Direction))
	}
	return pattern.Direction
}

// detectPatterns detects all supported patterns
func (s *PatternVolumeDeltaStrategy) detectPatterns(current, previous models.MarketData) models.Pattern {
	// Try patterns in order of preference
	if pattern := s.detectEngulfing(current, previous); pattern.Direction != models.SignalNone {
		return pattern
	}
	if pattern := s.detectHammer(current); pattern.Direction != models.SignalNone {
		return pattern
	}
	if pattern := s.detectInsideBar(current, previous); pattern.Direction != models.SignalNone {
		return pattern
	}
	if pattern := s.detectBreakout(current); pattern.Direction != models.SignalNone {
		return pattern
	}
	if pattern := s.detectMomentumCandle(current); pattern.Direction != models.SignalNone {
		return pattern
	}

	return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
}

// detectEngulfing detects engulfing pattern
func (s *PatternVolumeDeltaStrategy) detectEngulfing(current, previous models.MarketData) models.Pattern {
	currentBody := math.Abs(current.KLine.Close - current.KLine.Open)
	previousBody := math.Abs(previous.KLine.Close - previous.KLine.Open)

	// Bullish engulfing: current green candle completely engulfs previous red candle
	if current.KLine.Close > current.KLine.Open && previous.KLine.Close < previous.KLine.Open {
		if current.KLine.Open < previous.KLine.Close && current.KLine.Close > previous.KLine.Open &&
			currentBody > previousBody {
			return models.Pattern{
				Direction:  models.SignalLongEntry,
				Confidence: 0.8,
				Name:       "Bullish Engulfing",
			}
		}
	}

	// Bearish engulfing: current red candle completely engulfs previous green candle
	if current.KLine.Close < current.KLine.Open && previous.KLine.Close > previous.KLine.Open {
		if current.KLine.Open > previous.KLine.Close && current.KLine.Close < previous.KLine.Open &&
			currentBody > previousBody {
			return models.Pattern{
				Direction:  models.SignalShortEntry,
				Confidence: 0.8,
				Name:       "Bearish Engulfing",
			}
		}
	}

	return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
}

// detectHammer detects hammer or inverted hammer pattern
func (s *PatternVolumeDeltaStrategy) detectHammer(candle models.MarketData) models.Pattern {
	body := math.Abs(candle.KLine.Close - candle.KLine.Open)
	upperShadow := candle.KLine.High - math.Max(candle.KLine.Open, candle.KLine.Close)
	lowerShadow := math.Min(candle.KLine.Open, candle.KLine.Close) - candle.KLine.Low
	totalRange := candle.KLine.High - candle.KLine.Low

	if totalRange == 0 {
		return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
	}

	// Hammer: small body, long lower shadow
	if body < totalRange*0.3 && lowerShadow >= body*s.hRatio {
		// Bullish if close is in upper half
		if candle.KLine.Close > (candle.KLine.High+candle.KLine.Low)/2 {
			return models.Pattern{
				Direction:  models.SignalLongEntry,
				Confidence: 0.7,
				Name:       "Hammer",
			}
		}
	}

	// Inverted Hammer: small body, long upper shadow
	if body < totalRange*0.3 && upperShadow >= body*s.hRatio {
		// Bullish if close is in upper half
		if candle.KLine.Close > (candle.KLine.High+candle.KLine.Low)/2 {
			return models.Pattern{
				Direction:  models.SignalLongEntry,
				Confidence: 0.7,
				Name:       "Inverted Hammer",
			}
		}
	}

	return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
}

// detectInsideBar detects inside bar pattern
func (s *PatternVolumeDeltaStrategy) detectInsideBar(current, previous models.MarketData) models.Pattern {
	// Inside bar: current high < previous high and current low > previous low
	if current.KLine.High < previous.KLine.High && current.KLine.Low > previous.KLine.Low {
		// Wait for breakout - return NONE for now, could be enhanced to track breakout
		// For now, we'll use it as a neutral pattern that requires other confirmation
		return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "Inside Bar"}
	}
	return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
}

// detectBreakout detects breakout pattern
func (s *PatternVolumeDeltaStrategy) detectBreakout(current models.MarketData) models.Pattern {
	if len(s.history) < s.bLookback+1 {
		return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
	}

	// Find highest high and lowest low in lookback period
	high := current.KLine.High
	low := current.KLine.Low
	for i := len(s.history) - 2; i >= len(s.history)-s.bLookback-1 && i >= 0; i-- {
		if s.history[i].KLine.High > high {
			high = s.history[i].KLine.High
		}
		if s.history[i].KLine.Low < low {
			low = s.history[i].KLine.Low
		}
	}

	// Bullish breakout: close breaks above recent high
	if current.KLine.Close > high && current.KLine.Volume > 0 {
		return models.Pattern{
			Direction:  models.SignalLongEntry,
			Confidence: 0.75,
			Name:       "Breakout",
		}
	}

	// Bearish breakout: close breaks below recent low
	if current.KLine.Close < low && current.KLine.Volume > 0 {
		return models.Pattern{
			Direction:  models.SignalShortEntry,
			Confidence: 0.75,
			Name:       "Breakout",
		}
	}

	return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
}

// detectMomentumCandle detects momentum candle pattern
func (s *PatternVolumeDeltaStrategy) detectMomentumCandle(candle models.MarketData) models.Pattern {
	body := math.Abs(candle.KLine.Close - candle.KLine.Open)
	totalRange := candle.KLine.High - candle.KLine.Low

	if totalRange == 0 {
		return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
	}

	// Momentum candle: body >= totalRange * mRatio
	if body >= totalRange*s.mRatio && candle.KLine.Volume > 0 {
		if candle.KLine.Close > candle.KLine.Open {
			return models.Pattern{
				Direction:  models.SignalLongEntry,
				Confidence: 0.7,
				Name:       "Momentum Candle",
			}
		} else {
			return models.Pattern{
				Direction:  models.SignalShortEntry,
				Confidence: 0.7,
				Name:       "Momentum Candle",
			}
		}
	}

	return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
}

// checkVolume checks if volume meets the threshold
func (s *PatternVolumeDeltaStrategy) checkVolume(candle models.MarketData) bool {
	// 如果历史数据不足，使用可用数据计算平均值
	availableData := len(s.history) - 1 // 排除当前K线
	if availableData < 1 {
		if s.verboseLogging {
			log.Printf("策略过滤: Volume过滤 - 历史数据不足 (需要至少1根, 当前: %d)", availableData)
		}
		return false
	}

	// 使用可用的历史数据计算平均成交量（至少需要2根数据）
	lookback := s.vLookback
	if availableData < lookback {
		lookback = availableData
		if s.verboseLogging {
			log.Printf("策略过滤: Volume过滤 - 使用可用数据计算 (需要: %d, 可用: %d)", s.vLookback, availableData)
		}
	}

	// Calculate average volume over lookback period
	var sumVolume float64
	startIdx := len(s.history) - lookback - 1 // -1 to exclude current candle
	if startIdx < 0 {
		startIdx = 0
	}
	for i := startIdx; i < len(s.history)-1; i++ {
		sumVolume += s.history[i].KLine.Volume
	}
	avgVolume := sumVolume / float64(lookback)
	threshold := avgVolume * s.vMult

	// Check if current volume >= avg * multiplier
	result := candle.KLine.Volume >= threshold
	if !result {
		if s.verboseLogging {
			log.Printf("策略过滤: Volume过滤 - 当前成交量 %.2f < 阈值 %.2f (平均成交量: %.2f × 倍数: %.2f)",
				candle.KLine.Volume, threshold, avgVolume, s.vMult)
		}
	}
	return result
}

// checkDelta checks if delta meets the threshold
func (s *PatternVolumeDeltaStrategy) checkDelta(delta models.Delta, patternDirection models.SignalType) bool {
	var threshold float64

	if s.deltaThreshMode == "dynamic" {
		// Calculate dynamic threshold based on recent delta history
		if len(s.deltaHistory) < 10 {
			if s.verboseLogging {
				log.Printf("策略过滤: Delta过滤 - 历史数据不足 (需要: 10, 当前: %d)", len(s.deltaHistory))
			}
			return false
		}

		var sumAbs float64
		for _, d := range s.deltaHistory {
			sumAbs += math.Abs(d)
		}
		meanAbs := sumAbs / float64(len(s.deltaHistory))
		threshold = meanAbs * s.deltaDynMult
		if s.verboseLogging {
			log.Printf("策略过滤: Delta过滤 - 动态阈值: %.2f (平均绝对值: %.2f × 倍数: %.2f)", threshold, meanAbs, s.deltaDynMult)
		}
	} else {
		threshold = s.deltaThreshAbs
		if s.verboseLogging {
			log.Printf("策略过滤: Delta过滤 - 绝对阈值: %.2f", threshold)
		}
	}

	// Check delta direction matches pattern direction
	var result bool
	if patternDirection == models.SignalLongEntry {
		result = delta.Value >= threshold
		if !result {
			if s.verboseLogging {
				log.Printf("策略过滤: Delta过滤 - LONG方向: Delta值 %.2f < 阈值 %.2f", delta.Value, threshold)
			}
		}
	} else if patternDirection == models.SignalShortEntry {
		result = delta.Value <= -threshold
		if !result {
			if s.verboseLogging {
				log.Printf("策略过滤: Delta过滤 - SHORT方向: Delta值 %.2f > -阈值 %.2f (需要 <= %.2f)", delta.Value, threshold, -threshold)
			}
		}
	} else {
		return false
	}

	return result
}

// checkTrend checks if trend filter is satisfied
func (s *PatternVolumeDeltaStrategy) checkTrend(candle models.MarketData, patternDirection models.SignalType) bool {
	// Use EMA30 to determine trend
	ema30 := candle.Indicators.EMA30
	if ema30 == 0 {
		return false
	}

	if patternDirection == models.SignalLongEntry {
		// For long, price should be above EMA30
		return candle.KLine.Close > ema30
	} else if patternDirection == models.SignalShortEntry {
		// For short, price should be below EMA30
		return candle.KLine.Close < ema30
	}

	return false
}

// GetCurrentPattern returns the most recent detected pattern
func (s *PatternVolumeDeltaStrategy) GetCurrentPattern() models.Pattern {
	if len(s.history) < 2 {
		return models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
	}

	current := s.history[len(s.history)-1]
	previous := s.history[len(s.history)-2]
	return s.detectPatterns(current, previous)
}

// getPatternDirectionName converts SignalType to string for logging
func getPatternDirectionName(direction models.SignalType) string {
	switch direction {
	case models.SignalLongEntry:
		return "LONG"
	case models.SignalShortEntry:
		return "SHORT"
	default:
		return "NONE"
	}
}
