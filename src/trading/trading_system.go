package trading

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"vagues-go/src/backpack"
	"vagues-go/src/indicators"
	"vagues-go/src/models"
	"vagues-go/src/strategy"
)

// TradingSystem represents the main trading system
type TradingSystem struct {
	client        *backpack.Client
	strategy      *strategy.PatternVolumeDeltaStrategy
	orderManager  *OrderManager
	calculator    *indicators.Calculator
	symbol        string
	interval      string
	quantity      float64 // 保留用于兼容
	stopLossPct   float64
	takeProfitPct float64
	leverage      int     // 杠杆倍数
	maxPosPct     float64 // 单笔最大仓位占总权益比例
	// Delta tracking
	deltaHistory []models.Delta // History of delta values
}

// Config holds trading system configuration
type Config struct {
	Symbol        string
	Interval      string
	Quantity      float64 // 保留用于兼容，实际使用动态计算
	StopLossPct   float64
	TakeProfitPct float64
	Leverage      int     // 杠杆倍数（可选，默认为1，即无杠杆）
	MaxPosPct     float64 // 单笔最大仓位占总权益比例（默认2%）
}

// NewTradingSystem creates a new trading system
func NewTradingSystem(client *backpack.Client, config Config) *TradingSystem {
	// 默认杠杆为1（无杠杆）
	leverage := config.Leverage
	if leverage <= 0 {
		leverage = 1
	}

	// 默认最大仓位为2%
	maxPosPct := config.MaxPosPct
	if maxPosPct <= 0 {
		maxPosPct = 0.02 // 2%
	}

	return &TradingSystem{
		client:        client,
		strategy:      strategy.NewPatternVolumeDeltaStrategy(),
		orderManager:  NewOrderManager(),
		calculator:    indicators.NewCalculator(30),
		symbol:        config.Symbol,
		interval:      config.Interval,
		quantity:      config.Quantity,
		stopLossPct:   config.StopLossPct,
		takeProfitPct: config.TakeProfitPct,
		leverage:      leverage,
		maxPosPct:     maxPosPct,
		deltaHistory:  make([]models.Delta, 0),
	}
}

// Run starts the trading system
func (ts *TradingSystem) Run(ctx context.Context) error {
	log.Printf("启动交易系统 - 交易对: %s, 周期: %s, 杠杆: %dx", ts.symbol, ts.interval, ts.leverage)

	// 设置杠杆
	if ts.leverage > 1 {
		log.Printf("正在设置杠杆为 %dx...", ts.leverage)
		if err := ts.client.SetLeverage(ctx, ts.leverage); err != nil {
			log.Printf("⚠️  设置杠杆失败: %v (将使用账户当前杠杆设置)", err)
		} else {
			log.Printf("✅ 杠杆设置成功: %dx", ts.leverage)
		}
	} else {
		log.Printf("使用无杠杆交易 (杠杆: 1x)")
	}

	// 获取历史K线数据（至少需要满足Volume过滤的20根+缓冲）
	klines, err := ts.fetchHistoricalKlines(ctx, 50)
	if err != nil {
		return fmt.Errorf("获取历史K线数据失败: %w", err)
	}

	// 计算技术指标
	calculatedIndicators := ts.calculator.CalculateIndicators(klines)
	if len(calculatedIndicators) == 0 {
		return fmt.Errorf("无法计算技术指标，数据不足")
	}

	// 创建市场数据
	marketData := make([]models.MarketData, len(klines))
	for i := range klines {
		marketData[i] = models.MarketData{
			KLine:      klines[i],
			Indicators: calculatedIndicators[i],
		}
	}

	// 输出初始状态和指标
	if len(marketData) > 0 {
		delta := ts.calculateDelta(marketData[len(marketData)-1].KLine, klines)
		pattern := models.Pattern{Direction: models.SignalNone, Confidence: 0.0, Name: "None"}
		accountBalance, quoteAsset := ts.getAccountBalance(ctx)
		ts.printStatus(ctx, marketData[len(marketData)-1], models.SignalNone, pattern, delta, accountBalance, quoteAsset)
	}

	// 主交易循环
	ticker := time.NewTicker(ts.getIntervalDuration())
	defer ticker.Stop()

	// 持仓监控循环：每10秒检查一次持仓盈利状态和止盈止损
	positionMonitorTicker := time.NewTicker(10 * time.Second)
	defer positionMonitorTicker.Stop()

	// 启动持仓监控goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-positionMonitorTicker.C:
				if err := ts.checkPositionStatus(ctx); err != nil {
					log.Printf("检查持仓状态失败: %v", err)
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			log.Println("交易系统停止")
			return nil
		case <-ticker.C:
			if err := ts.processNewData(ctx); err != nil {
				log.Printf("处理新数据失败: %v", err)
			}
		}
	}
}

// processNewData processes new market data
func (ts *TradingSystem) processNewData(ctx context.Context) error {
	// 获取最新的K线数据
	klines, err := ts.fetchLatestKlines(ctx, 1)
	if err != nil {
		return fmt.Errorf("获取最新K线数据失败: %w", err)
	}

	if len(klines) == 0 {
		return fmt.Errorf("未获取到K线数据")
	}

	latestKline := klines[0]

	// 获取历史数据来计算指标（至少需要满足Volume过滤的20根+缓冲）
	historicalKlines, err := ts.fetchHistoricalKlines(ctx, 50)
	if err != nil {
		return fmt.Errorf("获取历史K线数据失败: %w", err)
	}

	// 计算技术指标
	calculatedIndicators := ts.calculator.CalculateIndicators(historicalKlines)
	if len(calculatedIndicators) == 0 {
		return fmt.Errorf("无法计算技术指标")
	}

	// 创建当前市场数据
	currentData := models.MarketData{
		KLine:      latestKline,
		Indicators: calculatedIndicators[len(calculatedIndicators)-1],
	}

	// 计算Delta（简化版本：基于K线数据估算）
	// 注意：真实实现需要逐笔交易数据，这里使用K线数据估算
	delta := ts.calculateDelta(latestKline, historicalKlines)

	// 分析市场信号
	signal := ts.strategy.Analyze(currentData, delta)

	// 获取当前pattern信息
	pattern := ts.strategy.GetCurrentPattern()

	// 获取账户余额
	accountBalance, quoteAsset := ts.getAccountBalance(ctx)

	// 输出当前状态和指标
	ts.printStatus(ctx, currentData, signal, pattern, delta, accountBalance, quoteAsset)

	// 处理交易信号
	switch signal {
	case models.SignalLongEntry:
		return ts.handleLongEntry(ctx, currentData)
	case models.SignalShortEntry:
		return ts.handleShortEntry(ctx, currentData)
	case models.SignalLongExit:
		return ts.handleLongExit(ctx, currentData)
	case models.SignalShortExit:
		return ts.handleShortExit(ctx, currentData)
	}

	// 检查止损止盈
	ts.orderManager.CheckStopLossTakeProfit(currentData.KLine.Close)

	return nil
}

// checkPositionStatus checks position profit/loss status and stop loss/take profit conditions
// This function is called every 10 seconds to monitor positions
func (ts *TradingSystem) checkPositionStatus(ctx context.Context) error {
	// 获取本地开仓订单
	openOrders := ts.orderManager.GetOpenOrders()
	if len(openOrders) == 0 {
		// 没有持仓，无需检查
		return nil
	}

	// 获取当前持仓信息（通过API）
	futuresSymbol := ts.getFuturesSymbol()
	positions, err := ts.client.GetPositions(ctx, futuresSymbol)
	if err != nil {
		return fmt.Errorf("获取持仓信息失败: %w", err)
	}

	// 如果没有持仓，但本地有订单，可能需要同步
	if len(positions) == 0 {
		log.Printf("⚠️  API显示无持仓，但本地有开仓订单，可能需要同步")
		return nil
	}

	// 获取当前标记价格（用于计算盈亏）
	var currentPrice float64
	for _, pos := range positions {
		if pos.Symbol == futuresSymbol {
			markPrice, err := strconv.ParseFloat(pos.MarkPrice, 64)
			if err != nil {
				log.Printf("警告: 无法解析标记价格: %s", pos.MarkPrice)
				continue
			}
			currentPrice = markPrice

			// 解析未实现盈亏
			unrealizedPnl, _ := strconv.ParseFloat(pos.UnrealizedPnl, 64)
			log.Printf("📊 持仓监控 - 交易对: %s | 标记价格: %.4f | 未实现盈亏: %.4f",
				pos.Symbol, markPrice, unrealizedPnl)
			break
		}
	}

	if currentPrice == 0 {
		// 如果没有找到标记价格，尝试获取最新K线价格
		klines, err := ts.fetchLatestKlines(ctx, 1)
		if err != nil || len(klines) == 0 {
			return fmt.Errorf("无法获取当前价格")
		}
		currentPrice = klines[0].Close
		log.Printf("📊 持仓监控 - 使用K线收盘价: %.4f", currentPrice)
	}

	// 检查每个开仓订单的止盈止损条件
	closedOrderIDs := ts.orderManager.CheckStopLossTakeProfit(currentPrice)

	// 如果有订单被关闭，执行平仓操作
	for _, orderID := range closedOrderIDs {
		order := ts.orderManager.GetOrder(orderID)
		if order == nil {
			continue
		}

		log.Printf("🔄 检测到止盈/止损触发 - 订单ID: %s, 类型: %s, 入场价: %.4f, 当前价: %.4f, 盈亏: %.4f",
			orderID, order.OrderType, order.EntryPrice, currentPrice, order.PnL)

		// 执行平仓
		futuresSymbol := ts.getFuturesSymbol()
		log.Printf("正在通过API平仓 - 交易对: %s", futuresSymbol)
		orderResp, err := ts.client.ClosePosition(ctx, futuresSymbol)
		if err != nil {
			log.Printf("⚠️  API平仓失败: %v (订单已在本地标记为关闭)", err)
			// 订单已在本地关闭，即使API失败也继续
		} else {
			log.Printf("✅ API平仓成功 - API订单ID: %s, 本地订单ID: %s, 价格: %.4f, 盈亏: %.4f",
				orderResp.ID, orderID, currentPrice, order.PnL)
		}
	}

	return nil
}

// handleLongEntry handles long entry signal
func (ts *TradingSystem) handleLongEntry(ctx context.Context, data models.MarketData) error {
	openOrders := ts.orderManager.GetOpenOrders()
	if len(openOrders) > 0 {
		log.Printf("已有开仓订单，跳过开多信号")
		return nil
	}

	// 计算止损止盈价格
	stopLoss := data.KLine.Close * (1 - ts.stopLossPct/100)
	takeProfit := data.KLine.Close * (1 + ts.takeProfitPct/100)

	// 计算开仓数量：账户余额 * 杠杆 * 最大仓位比例 / 入场价格
	quantity, err := ts.calculatePositionSize(ctx, data.KLine.Close, stopLoss)
	if err != nil {
		return fmt.Errorf("计算仓位大小失败: %w", err)
	}

	if quantity <= 0 {
		log.Printf("计算出的仓位大小为0，跳过开仓")
		return nil
	}

	// 转换symbol为期货格式
	futuresSymbol := ts.getFuturesSymbol()

	// 在下单前设置杠杆
	if ts.leverage > 1 {
		log.Printf("下单前设置杠杆为 %dx...", ts.leverage)
		if err := ts.client.SetLeverage(ctx, ts.leverage); err != nil {
			log.Printf("⚠️  设置杠杆失败: %v (将使用账户当前杠杆设置)", err)
		} else {
			log.Printf("✅ 杠杆设置成功: %dx", ts.leverage)
		}
	}

	// 格式化数量，根据交易对调整精度
	// 对于大多数交易对，使用合理的小数位数（避免精度过长错误）
	// 先尝试4位小数，如果还是太长，可以进一步减少
	quantityStr := fmt.Sprintf("%.4f", quantity)
	// 移除尾部的0和小数点
	quantityStr = strings.TrimRight(quantityStr, "0")
	quantityStr = strings.TrimSuffix(quantityStr, ".")

	// 调用API开多仓（使用市价单）
	orderReq := backpack.OrderRequest{
		Symbol:      futuresSymbol,
		Side:        "Bid", // 买入/做多
		OrderType:   "Market",
		Quantity:    quantityStr,
		TimeInForce: "IOC", // 立即成交或取消
	}

	log.Printf("正在通过API开多仓 - 交易对: %s, 数量: %s (基于账户余额和杠杆计算)", futuresSymbol, quantityStr)
	orderResp, err := ts.client.PlaceOrder(ctx, orderReq)
	if err != nil {
		return fmt.Errorf("API开多仓失败: %w", err)
	}

	// 保存订单到本地管理器
	orderID := ts.orderManager.OpenLong(ts.symbol, data.KLine.Close, quantity, stopLoss, takeProfit)
	// 更新本地订单ID为API返回的订单ID
	ts.orderManager.UpdateOrderID(orderID, orderResp.ID)

	log.Printf("✅ 开多仓成功 - API订单ID: %s, 本地订单ID: %s, 价格: %.4f, 数量: %.4f, 止损: %.4f, 止盈: %.4f",
		orderResp.ID, orderID, data.KLine.Close, quantity, stopLoss, takeProfit)

	return nil
}

// handleShortEntry handles short entry signal
func (ts *TradingSystem) handleShortEntry(ctx context.Context, data models.MarketData) error {
	openOrders := ts.orderManager.GetOpenOrders()
	if len(openOrders) > 0 {
		log.Printf("已有开仓订单，跳过开空信号")
		return nil
	}

	// 计算止损止盈价格
	stopLoss := data.KLine.Close * (1 + ts.stopLossPct/100)
	takeProfit := data.KLine.Close * (1 - ts.takeProfitPct/100)

	// 计算开仓数量：账户余额 * 杠杆 * 最大仓位比例 / 入场价格
	quantity, err := ts.calculatePositionSize(ctx, data.KLine.Close, stopLoss)
	if err != nil {
		return fmt.Errorf("计算仓位大小失败: %w", err)
	}

	if quantity <= 0 {
		log.Printf("计算出的仓位大小为0，跳过开仓")
		return nil
	}

	// 转换symbol为期货格式
	futuresSymbol := ts.getFuturesSymbol()

	// 在下单前设置杠杆
	if ts.leverage > 1 {
		log.Printf("下单前设置杠杆为 %dx...", ts.leverage)
		if err := ts.client.SetLeverage(ctx, ts.leverage); err != nil {
			log.Printf("⚠️  设置杠杆失败: %v (将使用账户当前杠杆设置)", err)
		} else {
			log.Printf("✅ 杠杆设置成功: %dx", ts.leverage)
		}
	}

	// 格式化数量，根据交易对调整精度
	// 对于大多数交易对，使用合理的小数位数（避免精度过长错误）
	// 先尝试4位小数，如果还是太长，可以进一步减少
	quantityStr := fmt.Sprintf("%.4f", quantity)
	// 移除尾部的0和小数点
	quantityStr = strings.TrimRight(quantityStr, "0")
	quantityStr = strings.TrimSuffix(quantityStr, ".")

	// 调用API开空仓（使用市价单）
	orderReq := backpack.OrderRequest{
		Symbol:      futuresSymbol,
		Side:        "Ask", // 卖出/做空
		OrderType:   "Market",
		Quantity:    quantityStr,
		TimeInForce: "IOC", // 立即成交或取消
	}

	log.Printf("正在通过API开空仓 - 交易对: %s, 数量: %s (基于账户余额和杠杆计算)", futuresSymbol, quantityStr)
	orderResp, err := ts.client.PlaceOrder(ctx, orderReq)
	if err != nil {
		return fmt.Errorf("API开空仓失败: %w", err)
	}

	// 保存订单到本地管理器
	orderID := ts.orderManager.OpenShort(ts.symbol, data.KLine.Close, quantity, stopLoss, takeProfit)
	// 更新本地订单ID为API返回的订单ID
	ts.orderManager.UpdateOrderID(orderID, orderResp.ID)

	log.Printf("✅ 开空仓成功 - API订单ID: %s, 本地订单ID: %s, 价格: %.4f, 数量: %.4f, 止损: %.4f, 止盈: %.4f",
		orderResp.ID, orderID, data.KLine.Close, quantity, stopLoss, takeProfit)

	return nil
}

// handleLongExit handles long exit signal
func (ts *TradingSystem) handleLongExit(ctx context.Context, data models.MarketData) error {
	openOrders := ts.orderManager.GetOpenOrders()
	for _, order := range openOrders {
		if order.OrderType == OrderTypeLong {
			// 转换symbol为期货格式
			futuresSymbol := ts.getFuturesSymbol()

			// 调用API平仓
			log.Printf("正在通过API平多仓 - 交易对: %s, 数量: %.4f", futuresSymbol, order.Quantity)
			orderResp, err := ts.client.ClosePosition(ctx, futuresSymbol)
			if err != nil {
				log.Printf("API平多仓失败: %v", err)
				// 即使API失败，也更新本地订单状态
				ts.orderManager.CloseOrder(order.ID, data.KLine.Close)
				continue
			}

			// 更新本地订单状态
			if err := ts.orderManager.CloseOrder(order.ID, data.KLine.Close); err != nil {
				log.Printf("更新本地订单状态失败: %v", err)
			} else {
				log.Printf("✅ 平多仓成功 - API订单ID: %s, 本地订单ID: %s, 价格: %.4f, 盈亏: %.4f",
					orderResp.ID, order.ID, data.KLine.Close, order.PnL)
			}
		}
	}
	return nil
}

// handleShortExit handles short exit signal
func (ts *TradingSystem) handleShortExit(ctx context.Context, data models.MarketData) error {
	openOrders := ts.orderManager.GetOpenOrders()
	for _, order := range openOrders {
		if order.OrderType == OrderTypeShort {
			// 转换symbol为期货格式
			futuresSymbol := ts.getFuturesSymbol()

			// 调用API平仓
			log.Printf("正在通过API平空仓 - 交易对: %s, 数量: %.4f", futuresSymbol, order.Quantity)
			orderResp, err := ts.client.ClosePosition(ctx, futuresSymbol)
			if err != nil {
				log.Printf("API平空仓失败: %v", err)
				// 即使API失败，也更新本地订单状态
				ts.orderManager.CloseOrder(order.ID, data.KLine.Close)
				continue
			}

			// 更新本地订单状态
			if err := ts.orderManager.CloseOrder(order.ID, data.KLine.Close); err != nil {
				log.Printf("更新本地订单状态失败: %v", err)
			} else {
				log.Printf("✅ 平空仓成功 - API订单ID: %s, 本地订单ID: %s, 价格: %.4f, 盈亏: %.4f",
					orderResp.ID, order.ID, data.KLine.Close, order.PnL)
			}
		}
	}
	return nil
}

// fetchHistoricalKlines fetches historical K-line data
func (ts *TradingSystem) fetchHistoricalKlines(ctx context.Context, limit int) ([]models.KLine, error) {
	endTime := time.Now().Unix()
	startTime := endTime - int64(limit)*ts.getIntervalSeconds()

	klineResponses, err := ts.client.GetKlines(ctx, ts.symbol, ts.interval, &startTime, &endTime, &limit)
	if err != nil {
		return nil, err
	}

	klines := make([]models.KLine, len(klineResponses))
	for i, resp := range klineResponses {
		kline, err := ts.convertKlineResponse(resp)
		if err != nil {
			return nil, err
		}
		klines[i] = kline
	}

	if len(klines) > 0 {
		log.Printf("成功获取 %d 条历史K线数据 (时间范围: %s 至 %s)",
			len(klines),
			klines[0].StartTime.Format("2006-01-02 15:04:05"),
			klines[len(klines)-1].EndTime.Format("2006-01-02 15:04:05"))
	}

	return klines, nil
}

// fetchLatestKlines fetches the latest K-line data
func (ts *TradingSystem) fetchLatestKlines(ctx context.Context, limit int) ([]models.KLine, error) {
	// 使用当前时间作为结束时间，根据limit计算开始时间
	endTime := time.Now().Unix()
	startTime := endTime - int64(limit)*ts.getIntervalSeconds()

	klineResponses, err := ts.client.GetKlines(ctx, ts.symbol, ts.interval, &startTime, &endTime, &limit)
	if err != nil {
		return nil, err
	}

	klines := make([]models.KLine, len(klineResponses))
	for i, resp := range klineResponses {
		kline, err := ts.convertKlineResponse(resp)
		if err != nil {
			return nil, err
		}
		klines[i] = kline
	}

	if len(klines) > 0 {
		log.Printf("成功获取 %d 条最新K线数据 (最新时间: %s)",
			len(klines),
			klines[0].EndTime.Format("2006-01-02 15:04:05"))
	}

	return klines, nil
}

// convertKlineResponse converts backpack KlineResponse to models.KLine
func (ts *TradingSystem) convertKlineResponse(resp backpack.KlineResponse) (models.KLine, error) {
	// Parse time - try multiple formats
	// Format 1: "2006-01-02 15:04:05" (space-separated)
	// Format 2: "2006-01-02T15:04:05Z" (RFC3339)
	timeFormats := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05",
	}

	var startTime, endTime time.Time
	var err error

	// Parse start time
	for _, format := range timeFormats {
		startTime, err = time.Parse(format, resp.Start)
		if err == nil {
			break
		}
	}
	if err != nil {
		return models.KLine{}, fmt.Errorf("解析开始时间失败: %w (时间字符串: %s)", err, resp.Start)
	}

	// Parse end time
	for _, format := range timeFormats {
		endTime, err = time.Parse(format, resp.End)
		if err == nil {
			break
		}
	}
	if err != nil {
		return models.KLine{}, fmt.Errorf("解析结束时间失败: %w (时间字符串: %s)", err, resp.End)
	}

	// Parse prices and volumes
	open, _ := strconv.ParseFloat(resp.Open, 64)
	high, _ := strconv.ParseFloat(resp.High, 64)
	low, _ := strconv.ParseFloat(resp.Low, 64)
	close, _ := strconv.ParseFloat(resp.Close, 64)
	volume, _ := strconv.ParseFloat(resp.Volume, 64)
	quoteVolume, _ := strconv.ParseFloat(resp.QuoteVolume, 64)

	return models.KLine{
		StartTime:   startTime,
		EndTime:     endTime,
		Open:        open,
		High:        high,
		Low:         low,
		Close:       close,
		Volume:      volume,
		QuoteVolume: quoteVolume,
	}, nil
}

// getIntervalDuration returns the duration for the trading interval
func (ts *TradingSystem) getIntervalDuration() time.Duration {
	switch ts.interval {
	case "1m":
		return time.Minute
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "1h":
		return time.Hour
	case "4h":
		return 4 * time.Hour
	case "1d":
		return 24 * time.Hour
	default:
		return 5 * time.Minute // Default to 5 minutes
	}
}

// getIntervalSeconds returns the interval in seconds
func (ts *TradingSystem) getIntervalSeconds() int64 {
	switch ts.interval {
	case "1m":
		return 60
	case "5m":
		return 300
	case "15m":
		return 900
	case "1h":
		return 3600
	case "4h":
		return 14400
	case "1d":
		return 86400
	default:
		return 300 // Default to 5 minutes
	}
}

// getFuturesSymbol converts spot symbol to futures symbol format
// e.g., "SOL_USDC" -> "SOL_USDC_PERP"
func (ts *TradingSystem) getFuturesSymbol() string {
	// 如果已经是期货格式，直接返回
	if strings.HasSuffix(ts.symbol, "_PERP") {
		return ts.symbol
	}
	// 添加_PERP后缀
	return ts.symbol + "_PERP"
}

// calculatePositionSize calculates position size based on account balance, leverage, and risk management
// Formula: (Account Balance * Leverage * Max Position %) / Entry Price
func (ts *TradingSystem) calculatePositionSize(ctx context.Context, entryPrice, stopLossPrice float64) (float64, error) {
	// 获取账户余额（复用getAccountBalance方法）
	accountBalance, quoteAsset := ts.getAccountBalance(ctx)

	if accountBalance <= 0 {
		return 0, fmt.Errorf("账户 %s 余额不足或为0", quoteAsset)
	}

	log.Printf("账户余额: %.4f %s, 杠杆: %dx, 最大仓位比例: %.2f%%",
		accountBalance, quoteAsset, ts.leverage, ts.maxPosPct*100)

	// 计算可用资金：账户余额 * 杠杆
	availableCapital := accountBalance * float64(ts.leverage)

	// 计算最大可用仓位金额：可用资金 * 最大仓位比例
	maxPositionValue := availableCapital * ts.maxPosPct

	// 计算开仓数量：最大仓位金额 / 入场价格
	quantity := maxPositionValue / entryPrice

	// 可选：根据止损距离进一步调整仓位（风险固定法）
	// 如果启用了这个功能，可以根据止损百分比调整
	stopLossDistance := math.Abs(entryPrice-stopLossPrice) / entryPrice
	if stopLossDistance > 0 {
		// 风险固定法：确保止损损失不超过账户的某个百分比
		// 这里保持简单，直接使用最大仓位比例
	}

	log.Printf("计算仓位: 可用资金=%.4f, 最大仓位金额=%.4f, 入场价格=%.4f, 开仓数量=%.4f",
		availableCapital, maxPositionValue, entryPrice, quantity)

	return quantity, nil
}

// getAccountBalance gets account balance for the quote asset using backpack client
func (ts *TradingSystem) getAccountBalance(ctx context.Context) (float64, string) {
	// 通过backpack客户端获取账户余额
	balances, err := ts.client.GetBalances(ctx)
	if err != nil {
		log.Printf("警告: 获取账户余额失败: %v", err)
		return 0, "USD"
	}

	if len(balances) == 0 {
		log.Printf("警告: 账户余额列表为空")
		return 0, "USD"
	}

	// 从symbol提取计价资产
	// 格式示例: SOL_USDC -> USDC, XPL_USDC_PERP -> USDC
	quoteAsset := "USD" // 默认使用USD
	parts := strings.Split(ts.symbol, "_")

	if len(parts) >= 2 {
		// 如果最后一部分是PERP，取倒数第二部分；否则取最后一部分
		lastPart := parts[len(parts)-1]
		if lastPart == "PERP" && len(parts) >= 3 {
			// 格式: XPL_USDC_PERP -> 取 USDC
			quoteAsset = parts[len(parts)-2]
		} else {
			// 格式: SOL_USDC -> 取 USDC
			quoteAsset = lastPart
		}
	}

	log.Printf("正在查找计价资产: %s (从交易对 %s 提取)", quoteAsset, ts.symbol)

	// 查找计价资产余额（支持USD/USDC互匹配，优先查找USD）
	// 首先尝试精确匹配
	var matchedBalance *backpack.Balance
	var matchedAsset string

	// 第一优先级：精确匹配quoteAsset
	for i := range balances {
		if strings.EqualFold(balances[i].Asset, quoteAsset) {
			matchedBalance = &balances[i]
			matchedAsset = balances[i].Asset
			log.Printf("✅ 精确匹配到 %s", matchedAsset)
			break
		}
	}

	// 第二优先级：如果quoteAsset是USDC，尝试查找USD（优先）
	if matchedBalance == nil && strings.EqualFold(quoteAsset, "USDC") {
		for i := range balances {
			if strings.EqualFold(balances[i].Asset, "USD") {
				available, _ := strconv.ParseFloat(balances[i].Available, 64)
				if available > 0 {
					matchedBalance = &balances[i]
					matchedAsset = "USD"
					log.Printf("✅ USDC未找到，使用USD余额")
					break
				}
			}
		}
	}

	// 第三优先级：如果quoteAsset是USD，尝试查找USDC
	if matchedBalance == nil && strings.EqualFold(quoteAsset, "USD") {
		for i := range balances {
			if strings.EqualFold(balances[i].Asset, "USDC") {
				available, _ := strconv.ParseFloat(balances[i].Available, 64)
				if available > 0 {
					matchedBalance = &balances[i]
					matchedAsset = "USDC"
					log.Printf("✅ USD未找到，使用USDC余额")
					break
				}
			}
		}
	}

	// 第四优先级：如果还没找到，尝试查找所有可能的USD变体（大小写不敏感）
	if matchedBalance == nil {
		usdVariants := []string{"USD", "USDC", "USDT", "USDP"}
		for _, variant := range usdVariants {
			for i := range balances {
				if strings.EqualFold(balances[i].Asset, variant) {
					available, _ := strconv.ParseFloat(balances[i].Available, 64)
					locked, _ := strconv.ParseFloat(balances[i].Locked, 64)
					staked, _ := strconv.ParseFloat(balances[i].Staked, 64)
					total := available + locked + staked
					if total > 0 {
						matchedBalance = &balances[i]
						matchedAsset = balances[i].Asset
						log.Printf("✅ 找到 %s 余额（总计: %.4f）", matchedAsset, total)
						break
					}
				}
			}
			if matchedBalance != nil {
				break
			}
		}
	}

	if matchedBalance == nil {
		log.Printf("警告: 未找到 %s/USD/USDC/USDT 余额，请检查账户中是否有该资产", quoteAsset)
		log.Printf("提示: 请检查原始API响应中是否有其他资产名称")
		return 0, quoteAsset
	}

	available, err := strconv.ParseFloat(matchedBalance.Available, 64)
	if err != nil {
		log.Printf("警告: 无法解析 %s 余额: %s", matchedAsset, matchedBalance.Available)
		return 0, matchedAsset
	}

	log.Printf("✅ 找到 %s 余额: %.4f (可用)", matchedAsset, available)
	return available, matchedAsset
}

// calculateDelta calculates order flow delta from K-line data
// Note: This is a simplified version. Real implementation requires tick-by-tick trade data
func (ts *TradingSystem) calculateDelta(currentKline models.KLine, historicalKlines []models.KLine) models.Delta {
	// Simplified delta calculation based on price movement and volume
	// If close > open, assume more buy pressure; if close < open, assume more sell pressure
	// This is an approximation - real delta requires aggressor side information from order book

	body := currentKline.Close - currentKline.Open
	totalRange := currentKline.High - currentKline.Low

	var buyVolume, sellVolume float64

	if totalRange > 0 {
		// Estimate buy/sell volume based on price movement
		// If close is in upper portion, more buy pressure
		upperPortion := (currentKline.Close - currentKline.Low) / totalRange

		// Simple heuristic: if close > open, more buy volume
		if body > 0 {
			buyVolume = currentKline.Volume * (0.5 + upperPortion*0.3)
			sellVolume = currentKline.Volume * (0.5 - upperPortion*0.3)
		} else {
			buyVolume = currentKline.Volume * (0.5 - upperPortion*0.3)
			sellVolume = currentKline.Volume * (0.5 + upperPortion*0.3)
		}
	} else {
		// No price movement, split volume equally
		buyVolume = currentKline.Volume * 0.5
		sellVolume = currentKline.Volume * 0.5
	}

	delta := models.Delta{
		Value:      buyVolume - sellVolume,
		BuyVolume:  buyVolume,
		SellVolume: sellVolume,
	}

	// Store in history
	ts.deltaHistory = append(ts.deltaHistory, delta)
	if len(ts.deltaHistory) > 100 {
		ts.deltaHistory = ts.deltaHistory[1:]
	}

	return delta
}

// GetPerformance returns trading performance statistics
func (ts *TradingSystem) GetPerformance() *PerformanceStats {
	closedOrders := ts.orderManager.GetClosedOrders()
	openOrders := ts.orderManager.GetOpenOrders()
	totalPnL := ts.orderManager.GetTotalPnL()

	return &PerformanceStats{
		TotalOrders:  len(closedOrders) + len(openOrders),
		ClosedOrders: len(closedOrders),
		OpenOrders:   len(openOrders),
		TotalPnL:     totalPnL,
		WinRate:      ts.calculateWinRate(closedOrders),
		AverageWin:   ts.calculateAverageWin(closedOrders),
		AverageLoss:  ts.calculateAverageLoss(closedOrders),
	}
}

// PerformanceStats holds trading performance statistics
type PerformanceStats struct {
	TotalOrders  int
	ClosedOrders int
	OpenOrders   int
	TotalPnL     float64
	WinRate      float64
	AverageWin   float64
	AverageLoss  float64
}

// calculateWinRate calculates the win rate from closed orders
func (ts *TradingSystem) calculateWinRate(orders []*LocalOrder) float64 {
	if len(orders) == 0 {
		return 0
	}

	winCount := 0
	for _, order := range orders {
		if order.PnL > 0 {
			winCount++
		}
	}

	return float64(winCount) / float64(len(orders)) * 100
}

// calculateAverageWin calculates the average win amount
func (ts *TradingSystem) calculateAverageWin(orders []*LocalOrder) float64 {
	winOrders := make([]*LocalOrder, 0)
	for _, order := range orders {
		if order.PnL > 0 {
			winOrders = append(winOrders, order)
		}
	}

	if len(winOrders) == 0 {
		return 0
	}

	totalWin := 0.0
	for _, order := range winOrders {
		totalWin += order.PnL
	}

	return totalWin / float64(len(winOrders))
}

// calculateAverageLoss calculates the average loss amount
func (ts *TradingSystem) calculateAverageLoss(orders []*LocalOrder) float64 {
	lossOrders := make([]*LocalOrder, 0)
	for _, order := range orders {
		if order.PnL < 0 {
			lossOrders = append(lossOrders, order)
		}
	}

	if len(lossOrders) == 0 {
		return 0
	}

	totalLoss := 0.0
	for _, order := range lossOrders {
		totalLoss += order.PnL
	}

	return totalLoss / float64(len(lossOrders))
}

// printStatus prints the current market status and indicators
func (ts *TradingSystem) printStatus(ctx context.Context, data models.MarketData, signal models.SignalType, pattern models.Pattern, delta models.Delta, accountBalance float64, quoteAsset string) {
	kline := data.KLine
	ind := data.Indicators

	// 获取信号名称
	signalName := "无信号"
	switch signal {
	case models.SignalLongEntry:
		signalName = "开多信号"
	case models.SignalShortEntry:
		signalName = "开空信号"
	case models.SignalLongExit:
		signalName = "平多信号"
	case models.SignalShortExit:
		signalName = "平空信号"
	}

	// 获取持仓状态
	openOrders := ts.orderManager.GetOpenOrders()
	positionInfo := "无持仓"
	if len(openOrders) > 0 {
		order := openOrders[0]
		positionInfo = fmt.Sprintf("%s | 入场价: %.4f | 数量: %.4f | 止损: %.4f | 止盈: %.4f",
			order.OrderType, order.EntryPrice, order.Quantity, order.StopLoss, order.TakeProfit)
	}

	// 计算可用资金（账户余额 * 杠杆）
	availableCapital := accountBalance * float64(ts.leverage)

	// 输出状态信息
	log.Println("=" + strings.Repeat("=", 80))
	log.Printf("时间: %s | 交易对: %s | 周期: %s | 杠杆: %dx",
		kline.EndTime.Format("2006-01-02 15:04:05"), ts.symbol, ts.interval, ts.leverage)
	log.Printf("账户余额: %.4f %s | 可用资金(含杠杆): %.4f %s | 最大仓位比例: %.2f%%",
		accountBalance, quoteAsset, availableCapital, quoteAsset, ts.maxPosPct*100)
	log.Printf("价格: 开=%.4f 高=%.4f 低=%.4f 收=%.4f | 成交量: %.2f",
		kline.Open, kline.High, kline.Low, kline.Close, kline.Volume)
	log.Println("--- Pattern + Volume + Delta 策略 ---")
	log.Printf("Pattern: %s | 方向: %s | 置信度: %.2f",
		pattern.Name, getSignalName(pattern.Direction), pattern.Confidence)
	log.Printf("Delta: 值=%.2f | 买量=%.2f | 卖量=%.2f",
		delta.Value, delta.BuyVolume, delta.SellVolume)
	log.Println("--- 技术指标 ---")
	log.Printf("EMA30: %.4f (趋势过滤)", ind.EMA30)
	log.Printf("交易信号: %s", signalName)
	log.Printf("持仓状态: %s", positionInfo)

	// 输出总盈亏
	totalPnL := ts.orderManager.GetTotalPnL()
	closedOrders := ts.orderManager.GetClosedOrders()
	if len(closedOrders) > 0 || totalPnL != 0 {
		log.Printf("交易统计: 总盈亏=%.4f | 已平仓订单数=%d", totalPnL, len(closedOrders))
	}
	log.Println("=" + strings.Repeat("=", 80))
}

// getSignalName converts SignalType to string
func getSignalName(signal models.SignalType) string {
	switch signal {
	case models.SignalLongEntry:
		return "LONG"
	case models.SignalShortEntry:
		return "SHORT"
	default:
		return "NONE"
	}
}
