package models

import (
	"time"
)

// KLine represents a single candlestick data point
type KLine struct {
	StartTime   time.Time // 开始时间
	EndTime     time.Time // 结束时间
	Open        float64   // 开盘价
	High        float64   // 最高价
	Low         float64   // 最低价
	Close       float64   // 收盘价
	Volume      float64   // 成交量
	QuoteVolume float64   // 成交额
}

// Indicators contains all technical indicators calculated from K-line data
type Indicators struct {
	EMA8          float64 // 8周期EMA
	EMA30         float64 // 30周期EMA
	EMA55         float64 // 55周期EMA
	EMA144        float64 // 144周期EMA
	EMA169        float64 // 169周期EMA
	MACD          float64 // MACD值
	MACDSignal    float64 // MACD信号线
	MACDHistogram float64 // MACD柱状图
	RSI           float64 // RSI指标
}

// Pattern represents a detected price pattern
type Pattern struct {
	Direction  SignalType // LONG, SHORT, or NONE
	Confidence float64    // 0.0 to 1.0
	Name       string     // Pattern name (e.g., "Engulfing", "Hammer")
}

// Delta represents order flow delta (buy volume - sell volume)
type Delta struct {
	Value      float64 // Delta value
	BuyVolume  float64 // Aggressor buy volume
	SellVolume float64 // Aggressor sell volume
}

// MarketData combines K-line data with calculated indicators
type MarketData struct {
	KLine      KLine
	Indicators Indicators
}

// TrendDirection represents the market trend direction
type TrendDirection int

const (
	TrendNeutral TrendDirection = iota
	TrendBullish
	TrendBearish
)

// SignalType represents trading signals
type SignalType int

const (
	SignalNone SignalType = iota
	SignalLongEntry
	SignalShortEntry
	SignalLongExit
	SignalShortExit
)
