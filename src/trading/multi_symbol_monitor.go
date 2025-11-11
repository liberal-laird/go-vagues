package trading

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"vagues-go/src/backpack"
)

// MultiSymbolMonitor 多交易对监控系统
type MultiSymbolMonitor struct {
	client         *backpack.Client
	config         Config
	tradingSystems map[string]*TradingSystem // symbol -> TradingSystem
	mu             sync.RWMutex
	checkInterval  time.Duration
}

// NewMultiSymbolMonitor 创建多交易对监控系统
func NewMultiSymbolMonitor(client *backpack.Client, config Config) *MultiSymbolMonitor {
	return &MultiSymbolMonitor{
		client:         client,
		config:         config,
		tradingSystems: make(map[string]*TradingSystem),
		checkInterval:  1 * time.Minute, // 默认每分钟检查一次
	}
}

// Run 启动多交易对监控
func (m *MultiSymbolMonitor) Run(ctx context.Context) error {
	log.Println("=== 启动多交易对监控系统 ===")

	// 获取所有 PERP 交易对
	markets, err := m.client.GetMarkets(ctx)
	if err != nil {
		return fmt.Errorf("获取市场列表失败: %w", err)
	}

	// 过滤出 PERP 交易对
	var perpMarkets []backpack.Market
	for _, market := range markets {
		if market.MarketType == "PERP" && market.Visible && market.OrderBookState == "Open" {
			perpMarkets = append(perpMarkets, market)
		}
	}

	log.Printf("找到 %d 个 PERP 交易对", len(perpMarkets))

	if len(perpMarkets) == 0 {
		return fmt.Errorf("未找到可用的 PERP 交易对")
	}

	// 限制交易对数量（从环境变量 MAX_TRADING_SYMBOL 读取，默认20）
	maxSymbols := 20 // 默认值
	if m.config.MaxTradingSymbols > 0 {
		maxSymbols = m.config.MaxTradingSymbols
	}

	// 只取前 N 个交易对
	originalCount := len(perpMarkets)
	if len(perpMarkets) > maxSymbols {
		perpMarkets = perpMarkets[:maxSymbols]
		log.Printf("限制监控数量为 %d 个交易对（从 %d 个 PERP 交易对中选取前 %d 个）", maxSymbols, originalCount, maxSymbols)
	}

	log.Printf("开始监控 %d 个交易对...", len(perpMarkets))

	// 如果配置了杠杆，先统一设置一次（杠杆是账户级别的）
	if m.config.Leverage > 1 {
		log.Printf("正在为账户设置杠杆为 %dx...", m.config.Leverage)
		if err := m.client.SetLeverage(ctx, m.config.Leverage); err != nil {
			log.Printf("⚠️  设置杠杆失败: %v (将使用账户当前杠杆设置)", err)
		} else {
			log.Printf("✅ 账户杠杆设置成功: %dx", m.config.Leverage)
		}
	}

	// 为每个交易对创建独立的交易系统
	for _, market := range perpMarkets {
		symbolConfig := m.config
		symbolConfig.Symbol = market.Symbol

		ts := NewTradingSystem(m.client, symbolConfig)
		m.mu.Lock()
		m.tradingSystems[market.Symbol] = ts
		m.mu.Unlock()

		log.Printf("✅ 已添加交易对监控: %s", market.Symbol)
	}

	// 启动每个交易对的监控 goroutine
	var wg sync.WaitGroup
	for symbol, ts := range m.tradingSystems {
		wg.Add(1)
		go func(s string, tradingSys *TradingSystem) {
			defer wg.Done()
			log.Printf("🚀 启动交易对 %s 的监控...", s)
			// 创建独立的 context，但共享父 context 的取消信号
			symbolCtx := ctx
			if err := tradingSys.Run(symbolCtx); err != nil {
				log.Printf("交易对 %s 监控错误: %v", s, err)
			}
		}(symbol, ts)
	}

	// 等待所有 goroutine 完成
	wg.Wait()

	return nil
}

// GetTradingSystem 获取指定交易对的交易系统
func (m *MultiSymbolMonitor) GetTradingSystem(symbol string) (*TradingSystem, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ts, ok := m.tradingSystems[symbol]
	return ts, ok
}

// GetAllSymbols 获取所有监控的交易对
func (m *MultiSymbolMonitor) GetAllSymbols() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	symbols := make([]string, 0, len(m.tradingSystems))
	for symbol := range m.tradingSystems {
		symbols = append(symbols, symbol)
	}
	return symbols
}

// GetAllClosedOrders 获取所有交易系统的已平仓订单
func (m *MultiSymbolMonitor) GetAllClosedOrders() []*LocalOrder {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var allClosedOrders []*LocalOrder
	for _, ts := range m.tradingSystems {
		closedOrders := ts.orderManager.GetClosedOrders()
		allClosedOrders = append(allClosedOrders, closedOrders...)
	}

	return allClosedOrders
}
