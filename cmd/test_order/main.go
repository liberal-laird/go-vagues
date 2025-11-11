package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"vagues-go/src/backpack"

	"github.com/joho/godotenv"
)

func main() {
	// 加载 .env 文件
	if err := loadEnvFile(); err != nil {
		log.Printf("警告: 加载 .env 文件失败: %v", err)
	}

	// 初始化 Backpack 客户端（使用 NewClientFromEnv 自动从环境变量加载）
	client, err := backpack.NewClientFromEnv()
	if err != nil {
		log.Fatalf("创建客户端失败: %v\n请确保 .env 文件中设置了 BACKPACK_API_KEY 和 BACKPACK_API_SECRET", err)
	}

	ctx := context.Background()

	// 测试交易对（可以从命令行参数获取，默认使用 SOL_USDC_PERP）
	symbol := "SOL_USDC_PERP"
	if len(os.Args) > 1 {
		symbol = os.Args[1]
	}

	fmt.Printf("=== 测试交易对: %s ===\n\n", symbol)

	// 1. 获取当前持仓
	fmt.Println("1. 获取当前持仓信息...")
	positions, err := client.GetPositions(ctx, symbol)
	if err != nil {
		log.Printf("获取持仓失败: %v", err)
	} else {
		if len(positions) == 0 {
			fmt.Println("   当前无持仓")
		} else {
			for _, pos := range positions {
				fmt.Printf("   交易对: %s\n", pos.Symbol)
				fmt.Printf("   持仓数量: %s\n", pos.NetQuantity)
				fmt.Printf("   入场价格: %s\n", pos.EntryPrice)
				fmt.Printf("   标记价格: %s\n", pos.MarkPrice)
				fmt.Printf("   未实现盈亏: %s\n", pos.UnrealizedPnl)
			}
		}
	}
	fmt.Println()

	// 2. 获取账户余额
	fmt.Println("2. 获取账户余额...")
	balances, err := client.GetBalances(ctx)
	if err != nil {
		log.Printf("获取余额失败: %v", err)
	} else {
		for _, bal := range balances {
			if bal.Asset == "USDC" || bal.Asset == "USD" {
				available, _ := strconv.ParseFloat(bal.Available, 64)
				if available > 0 {
					fmt.Printf("   %s 可用余额: %s\n", bal.Asset, bal.Available)
				}
			}
		}
	}
	fmt.Println()

	// 3. 获取市场信息（stepSize）
	fmt.Println("3. 获取市场信息（stepSize）...")
	markets, err := client.GetMarkets(ctx)
	if err != nil {
		log.Printf("获取市场信息失败: %v", err)
	} else {
		for _, market := range markets {
			if market.Symbol == symbol {
				qf, err := market.GetQuantityFilter()
				if err == nil {
					fmt.Printf("   最小数量: %s\n", qf.MinQuantity)
					fmt.Printf("   步长: %s\n", qf.StepSize)
					if qf.MaxQuantity != "" {
						fmt.Printf("   最大数量: %s\n", qf.MaxQuantity)
					}
				}
				break
			}
		}
	}
	fmt.Println()

	// 4. 测试下单（如果当前无持仓）
	if len(positions) == 0 {
		fmt.Println("4. 测试开多仓（带止损止盈）...")
		fmt.Print("   请输入开仓数量（按回车使用默认值 0.1）: ")
		var quantityInput string
		fmt.Scanln(&quantityInput)

		quantity := 0.1
		if quantityInput != "" {
			if q, err := strconv.ParseFloat(quantityInput, 64); err == nil {
				quantity = q
			}
		}

		// 获取当前价格（用于计算止损止盈）
		var currentPrice float64
		for _, market := range markets {
			if market.Symbol == symbol {
				// 尝试从市场信息获取最新价格，如果没有则使用默认值
				// 这里简化处理，实际应该从ticker或最新K线获取
				currentPrice = 100.0 // 默认价格，实际应该从API获取
				break
			}
		}

		// 如果无法获取价格，提示用户输入
		if currentPrice == 0 {
			fmt.Print("   请输入当前价格（用于计算止损止盈，按回车使用默认值 100）: ")
			var priceInput string
			fmt.Scanln(&priceInput)
			if priceInput != "" {
				if p, err := strconv.ParseFloat(priceInput, 64); err == nil {
					currentPrice = p
				}
			} else {
				currentPrice = 100.0
			}
		}

		// 计算止损止盈价格（使用默认参数：止损0.25%，止盈0.6%）
		stopLossPct := 0.25
		takeProfitPct := 0.6
		stopLoss := currentPrice * (1 - stopLossPct/100)
		takeProfit := currentPrice * (1 + takeProfitPct/100)

		// 格式化数量
		quantityStr := formatQuantity(quantity, markets, symbol)
		fmt.Printf("   格式化后的数量: %s\n", quantityStr)

		// 格式化止损止盈价格
		stopLossStr := formatPrice(stopLoss, markets, symbol)
		takeProfitStr := formatPrice(takeProfit, markets, symbol)
		fmt.Printf("   当前价格: %.4f\n", currentPrice)
		fmt.Printf("   止损价格: %s (%.2f%%)\n", stopLossStr, stopLossPct)
		fmt.Printf("   止盈价格: %s (%.2f%%)\n", takeProfitStr, takeProfitPct)

		orderReq := backpack.OrderRequest{
			Symbol:                 symbol,
			Side:                   "Bid", // 买入/做多
			OrderType:              "Market",
			Quantity:               quantityStr,
			TimeInForce:            "IOC",         // 立即成交或取消
			StopLossTriggerPrice:   stopLossStr,   // 止损触发价格
			TakeProfitTriggerPrice: takeProfitStr, // 止盈触发价格
			StopLossTriggerBy:      "MarkPrice",   // 使用标记价格触发
			TakeProfitTriggerBy:    "MarkPrice",   // 使用标记价格触发
		}

		fmt.Printf("   正在下单: %s %s @ Market (止损: %s, 止盈: %s)...\n",
			quantityStr, symbol, stopLossStr, takeProfitStr)
		orderResp, err := client.PlaceOrder(ctx, orderReq)
		if err != nil {
			log.Printf("   ❌ 下单失败: %v", err)
		} else {
			fmt.Printf("   ✅ 下单成功! 订单ID: %s\n", orderResp.ID)
			fmt.Printf("   状态: %s\n", orderResp.Status)
			fmt.Printf("   ✅ 止损止盈已设置: 止损=%s, 止盈=%s\n", stopLossStr, takeProfitStr)
		}
		fmt.Println()

		// 等待一下，然后查看持仓和订单状态
		fmt.Println("   等待 3 秒后查看持仓和订单状态...")
		time.Sleep(3 * time.Second)

		positions, err = client.GetPositions(ctx, symbol)
		if err == nil && len(positions) > 0 {
			for _, pos := range positions {
				fmt.Printf("   当前持仓: %s @ %s (标记价格: %s)\n",
					pos.NetQuantity, pos.EntryPrice, pos.MarkPrice)
				fmt.Printf("   未实现盈亏: %s\n", pos.UnrealizedPnl)
			}
		}

		// 提示：止损止盈已通过API设置，交易所会自动监控
		fmt.Println()
		fmt.Println("   📌 止损止盈说明:")
		fmt.Printf("   - 止损价格: %s (标记价格下跌 %.2f%% 时自动平仓)\n", stopLossStr, stopLossPct)
		fmt.Printf("   - 止盈价格: %s (标记价格上涨 %.2f%% 时自动平仓)\n", takeProfitStr, takeProfitPct)
		fmt.Println("   - 止损止盈由交易所自动监控，无需程序持续运行")
		fmt.Println()
	}

	// 5. 测试平仓（如果有持仓）
	if len(positions) > 0 {
		fmt.Println("5. 测试平仓...")
		fmt.Print("   是否平仓? (y/n): ")
		var confirm string
		fmt.Scanln(&confirm)

		if confirm == "y" || confirm == "Y" {
			fmt.Printf("   正在平仓 %s...\n", symbol)
			orderResp, err := client.ClosePosition(ctx, symbol)
			if err != nil {
				log.Printf("   ❌ 平仓失败: %v", err)
			} else {
				fmt.Printf("   ✅ 平仓成功! 订单ID: %s\n", orderResp.ID)
				fmt.Printf("   状态: %s\n", orderResp.Status)
			}
			fmt.Println()

			// 等待一下，然后查看持仓
			fmt.Println("   等待 2 秒后查看持仓...")
			time.Sleep(2 * time.Second)

			positions, err = client.GetPositions(ctx, symbol)
			if err == nil {
				if len(positions) == 0 {
					fmt.Println("   ✅ 持仓已全部平仓")
				} else {
					fmt.Println("   ⚠️  仍有持仓:")
					for _, pos := range positions {
						fmt.Printf("      %s @ %s\n", pos.NetQuantity, pos.EntryPrice)
					}
				}
			}
		} else {
			fmt.Println("   跳过平仓")
		}
	}

	fmt.Println("\n=== 测试完成 ===")
}

// formatQuantity 格式化数量（根据 stepSize）
func formatQuantity(quantity float64, markets []backpack.Market, symbol string) string {
	for _, market := range markets {
		if market.Symbol == symbol {
			qf, err := market.GetQuantityFilter()
			if err == nil && qf.StepSize != "" {
				stepSize, err := strconv.ParseFloat(qf.StepSize, 64)
				if err == nil && stepSize > 0 {
					// 对齐到 stepSize
					alignedQuantity := float64(int(quantity/stepSize)) * stepSize
					// 确保不小于最小数量
					if qf.MinQuantity != "" {
						minQty, err := strconv.ParseFloat(qf.MinQuantity, 64)
						if err == nil && alignedQuantity < minQty {
							alignedQuantity = minQty
						}
					}
					// 计算小数位数
					decimals := countDecimals(stepSize)
					quantityStr := fmt.Sprintf("%."+fmt.Sprintf("%d", decimals)+"f", alignedQuantity)
					// 移除尾部的0
					quantityStr = strings.TrimRight(quantityStr, "0")
					quantityStr = strings.TrimSuffix(quantityStr, ".")
					return quantityStr
				}
			}
			break
		}
	}
	// 默认2位小数
	return fmt.Sprintf("%.2f", quantity)
}

// formatPrice 格式化价格（根据 tickSize）
func formatPrice(price float64, markets []backpack.Market, symbol string) string {
	for _, market := range markets {
		if market.Symbol == symbol {
			// 尝试获取 priceFilter
			if priceFilterData, ok := market.Filters["priceFilter"]; ok {
				// 将 priceFilterData 转换为 JSON 再解析
				filterJSON, err := json.Marshal(priceFilterData)
				if err == nil {
					var priceFilter struct {
						TickSize string `json:"tickSize"`
					}
					if err := json.Unmarshal(filterJSON, &priceFilter); err == nil && priceFilter.TickSize != "" {
						tickSize, err := strconv.ParseFloat(priceFilter.TickSize, 64)
						if err == nil && tickSize > 0 {
							// 将价格对齐到 tickSize 的倍数（向下取整）
							alignedPrice := float64(int(price/tickSize)) * tickSize
							// 计算小数位数
							decimals := countDecimals(tickSize)
							// 格式化
							priceStr := fmt.Sprintf("%."+fmt.Sprintf("%d", decimals)+"f", alignedPrice)
							// 移除尾部的0和小数点
							priceStr = strings.TrimRight(priceStr, "0")
							priceStr = strings.TrimSuffix(priceStr, ".")
							return priceStr
						}
					}
				}
			}
			break
		}
	}
	// 如果无法获取 tickSize，使用4位小数
	priceStr := fmt.Sprintf("%.4f", price)
	priceStr = strings.TrimRight(priceStr, "0")
	priceStr = strings.TrimSuffix(priceStr, ".")
	return priceStr
}

// countDecimals 计算小数位数
func countDecimals(value float64) int {
	str := fmt.Sprintf("%g", value)
	if !strings.Contains(str, ".") {
		return 0
	}
	parts := strings.Split(str, ".")
	if len(parts) != 2 {
		return 0
	}
	decimals := strings.TrimRight(parts[1], "0")
	return len(decimals)
}

// loadEnvFile 加载 .env 文件
func loadEnvFile() error {
	// 尝试多个可能的路径
	paths := []string{
		".env",
		"../.env",
		"../../.env",
		filepath.Join(os.Getenv("HOME"), ".env"),
	}

	for _, path := range paths {
		if err := godotenv.Load(path); err == nil {
			log.Printf("成功加载 .env 文件: %s", path)
			return nil
		}
	}

	return fmt.Errorf("未找到 .env 文件")
}
