package trading

import (
	"fmt"
	"time"
)

// OrderStatus represents the status of an order
type OrderStatus string

const (
	OrderStatusOpen     OrderStatus = "OPEN"
	OrderStatusFilled   OrderStatus = "FILLED"
	OrderStatusCanceled OrderStatus = "CANCELED"
	OrderStatusClosed   OrderStatus = "CLOSED"
)

// OrderType represents the type of order
type OrderType string

const (
	OrderTypeLong  OrderType = "LONG"
	OrderTypeShort OrderType = "SHORT"
)

// LocalOrder represents a locally tracked order
type LocalOrder struct {
	ID               string      // 订单ID
	Symbol           string      // 交易对
	OrderType        OrderType   // 订单类型（多/空）
	EntryPrice       float64     // 入场价格
	ExitPrice        float64     // 出场价格
	Quantity         float64     // 数量
	Status           OrderStatus // 订单状态
	EntryTime        time.Time   // 入场时间
	ExitTime         time.Time   // 出场时间
	StopLoss         float64     // 止损价格
	TakeProfit       float64     // 止盈价格
	TrailingStopLoss float64     // 追踪止损价格（动态更新）
	TrailingEnabled  bool        // 是否启用追踪止损
	TrailingPct      float64     // 追踪止损百分比（默认0.2%）
	MaxHoldBars      int         // 最大持仓K线数（默认12根1分钟）
	BarsHeld         int         // 已持仓K线数
	HighestPrice     float64     // 持仓期间最高价（用于追踪止损）
	LowestPrice      float64     // 持仓期间最低价（用于追踪止损）
	PnL              float64     // 盈亏金额
	PnLPercent       float64     // 盈亏百分比
	TradingFee       float64     // 交易手续费（开仓+平仓）
	FundingFee       float64     // 资金费率
}

// OrderManager manages local order tracking
type OrderManager struct {
	orders     map[string]*LocalOrder // 订单ID到订单的映射
	openOrders []string               // 当前开仓订单ID列表
	totalPnL   float64                // 总盈亏
}

// NewOrderManager creates a new order manager
func NewOrderManager() *OrderManager {
	return &OrderManager{
		orders:     make(map[string]*LocalOrder),
		openOrders: make([]string, 0),
		totalPnL:   0,
	}
}

// OpenLong opens a long position
func (om *OrderManager) OpenLong(symbol string, entryPrice, quantity float64, stopLoss, takeProfit float64) string {
	orderID := generateOrderID()
	order := &LocalOrder{
		ID:               orderID,
		Symbol:           symbol,
		OrderType:        OrderTypeLong,
		EntryPrice:       entryPrice,
		Quantity:         quantity,
		Status:           OrderStatusOpen,
		EntryTime:        time.Now(),
		StopLoss:         stopLoss,
		TakeProfit:       takeProfit,
		TrailingStopLoss: stopLoss, // Initialize to regular stop loss
		TrailingEnabled:  false,    // Will be enabled when reaching 50% TP
		TrailingPct:      0.002,    // 0.2% trailing stop
		MaxHoldBars:      12,       // 12 bars for 1m timeframe
		BarsHeld:         0,
		HighestPrice:     entryPrice,
		LowestPrice:      entryPrice,
	}

	om.orders[orderID] = order
	om.openOrders = append(om.openOrders, orderID)

	return orderID
}

// OpenShort opens a short position
func (om *OrderManager) OpenShort(symbol string, entryPrice, quantity float64, stopLoss, takeProfit float64) string {
	orderID := generateOrderID()
	order := &LocalOrder{
		ID:               orderID,
		Symbol:           symbol,
		OrderType:        OrderTypeShort,
		EntryPrice:       entryPrice,
		Quantity:         quantity,
		Status:           OrderStatusOpen,
		EntryTime:        time.Now(),
		StopLoss:         stopLoss,
		TakeProfit:       takeProfit,
		TrailingStopLoss: stopLoss, // Initialize to regular stop loss
		TrailingEnabled:  false,    // Will be enabled when reaching 50% TP
		TrailingPct:      0.002,    // 0.2% trailing stop
		MaxHoldBars:      12,       // 12 bars for 1m timeframe
		BarsHeld:         0,
		HighestPrice:     entryPrice,
		LowestPrice:      entryPrice,
	}

	om.orders[orderID] = order
	om.openOrders = append(om.openOrders, orderID)

	return orderID
}

// CloseOrder closes an open order
// tradingFee: 交易手续费（开仓+平仓）
// fundingFee: 资金费率
func (om *OrderManager) CloseOrder(orderID string, exitPrice float64, tradingFee, fundingFee float64) error {
	order, exists := om.orders[orderID]
	if !exists {
		return fmt.Errorf("订单不存在: %s", orderID)
	}

	if order.Status != OrderStatusOpen {
		return fmt.Errorf("订单状态不是开仓状态: %s", orderID)
	}

	order.ExitPrice = exitPrice
	order.ExitTime = time.Now()
	order.Status = OrderStatusClosed
	order.TradingFee = tradingFee
	order.FundingFee = fundingFee

	// 计算盈亏（减去手续费和资金费率）
	order.PnL = om.calculatePnL(order)
	order.PnLPercent = om.calculatePnLPercent(order)

	// 更新总盈亏
	om.totalPnL += order.PnL

	// 从开仓订单列表中移除
	om.removeOpenOrder(orderID)

	return nil
}

// CheckStopLossTakeProfit checks if any open orders hit stop loss, take profit, trailing stop, or timeout
func (om *OrderManager) CheckStopLossTakeProfit(currentPrice float64) []string {
	closedOrders := make([]string, 0)

	for _, orderID := range om.openOrders {
		order := om.orders[orderID]
		if order.Status != OrderStatusOpen {
			continue
		}

		// Update bars held
		order.BarsHeld++

		// Update highest/lowest price for trailing stop
		if currentPrice > order.HighestPrice {
			order.HighestPrice = currentPrice
		}
		if currentPrice < order.LowestPrice {
			order.LowestPrice = currentPrice
		}

		// Check if reached 50% of take profit to enable trailing stop
		if !order.TrailingEnabled {
			var profitPct float64
			if order.OrderType == OrderTypeLong {
				profitPct = (currentPrice - order.EntryPrice) / order.EntryPrice
			} else {
				profitPct = (order.EntryPrice - currentPrice) / order.EntryPrice
			}
			tpPct := (order.TakeProfit - order.EntryPrice) / order.EntryPrice
			if order.OrderType == OrderTypeShort {
				tpPct = (order.EntryPrice - order.TakeProfit) / order.EntryPrice
			}

			// Enable trailing stop when reaching 50% of TP
			if profitPct >= tpPct*0.5 {
				order.TrailingEnabled = true
				// Set initial trailing stop
				if order.OrderType == OrderTypeLong {
					order.TrailingStopLoss = currentPrice * (1 - order.TrailingPct)
				} else {
					order.TrailingStopLoss = currentPrice * (1 + order.TrailingPct)
				}
			}
		}

		// Update trailing stop if enabled
		if order.TrailingEnabled {
			if order.OrderType == OrderTypeLong {
				// For long: trailing stop moves up with price
				newTrailingStop := currentPrice * (1 - order.TrailingPct)
				if newTrailingStop > order.TrailingStopLoss {
					order.TrailingStopLoss = newTrailingStop
				}
			} else {
				// For short: trailing stop moves down with price
				newTrailingStop := currentPrice * (1 + order.TrailingPct)
				if newTrailingStop < order.TrailingStopLoss {
					order.TrailingStopLoss = newTrailingStop
				}
			}
		}

		// Check exit conditions
		var shouldClose bool

		switch order.OrderType {
		case OrderTypeLong:
			// Check take profit
			if currentPrice >= order.TakeProfit {
				shouldClose = true
			} else if order.TrailingEnabled && currentPrice <= order.TrailingStopLoss {
				// Check trailing stop
				shouldClose = true
			} else if currentPrice <= order.StopLoss {
				// Check regular stop loss
				shouldClose = true
			} else if order.BarsHeld >= order.MaxHoldBars {
				// Check timeout
				shouldClose = true
			}
		case OrderTypeShort:
			// Check take profit
			if currentPrice <= order.TakeProfit {
				shouldClose = true
			} else if order.TrailingEnabled && currentPrice >= order.TrailingStopLoss {
				// Check trailing stop
				shouldClose = true
			} else if currentPrice >= order.StopLoss {
				// Check regular stop loss
				shouldClose = true
			} else if order.BarsHeld >= order.MaxHoldBars {
				// Check timeout
				shouldClose = true
			}
		}

		if shouldClose {
			// 注意：已禁用自动平仓，此函数不会被调用
			// 如果被调用，手续费和资金费率设为0（实际应该从API获取）
			om.CloseOrder(orderID, currentPrice, 0, 0)
			closedOrders = append(closedOrders, orderID)
		}
	}

	return closedOrders
}

// GetOrder returns an order by ID
func (om *OrderManager) GetOrder(orderID string) *LocalOrder {
	if order, exists := om.orders[orderID]; exists {
		return order
	}
	return nil
}

// GetOpenOrders returns all open orders
func (om *OrderManager) GetOpenOrders() []*LocalOrder {
	openOrders := make([]*LocalOrder, 0, len(om.openOrders))
	for _, orderID := range om.openOrders {
		if order, exists := om.orders[orderID]; exists && order.Status == OrderStatusOpen {
			openOrders = append(openOrders, order)
		}
	}
	return openOrders
}

// GetClosedOrders returns all closed orders
func (om *OrderManager) GetClosedOrders() []*LocalOrder {
	closedOrders := make([]*LocalOrder, 0)
	for _, order := range om.orders {
		if order.Status == OrderStatusClosed {
			closedOrders = append(closedOrders, order)
		}
	}
	return closedOrders
}

// GetTotalPnL returns the total profit/loss
func (om *OrderManager) GetTotalPnL() float64 {
	return om.totalPnL
}

// calculatePnL calculates the profit/loss for an order
// 盈亏 = 价格差收益 - 交易手续费 - 资金费率
func (om *OrderManager) calculatePnL(order *LocalOrder) float64 {
	var pricePnL float64
	switch order.OrderType {
	case OrderTypeLong:
		pricePnL = (order.ExitPrice - order.EntryPrice) * order.Quantity
	case OrderTypeShort:
		pricePnL = (order.EntryPrice - order.ExitPrice) * order.Quantity
	default:
		return 0
	}

	// 减去交易手续费和资金费率
	return pricePnL - order.TradingFee - order.FundingFee
}

// calculatePnLPercent calculates the profit/loss percentage for an order
func (om *OrderManager) calculatePnLPercent(order *LocalOrder) float64 {
	if order.EntryPrice == 0 {
		return 0
	}
	return (order.PnL / (order.EntryPrice * order.Quantity)) * 100
}

// removeOpenOrder removes an order from the open orders list
func (om *OrderManager) removeOpenOrder(orderID string) {
	for i, id := range om.openOrders {
		if id == orderID {
			om.openOrders = append(om.openOrders[:i], om.openOrders[i+1:]...)
			break
		}
	}
}

// UpdateOrderID updates the order ID from local ID to API order ID
func (om *OrderManager) UpdateOrderID(oldID, newID string) {
	order, exists := om.orders[oldID]
	if !exists {
		return
	}

	// 更新订单ID
	order.ID = newID
	om.orders[newID] = order
	delete(om.orders, oldID)

	// 更新openOrders列表中的ID
	for i, id := range om.openOrders {
		if id == oldID {
			om.openOrders[i] = newID
			break
		}
	}
}

// generateOrderID generates a unique order ID
func generateOrderID() string {
	return fmt.Sprintf("LOCAL_%d", time.Now().UnixNano())
}
