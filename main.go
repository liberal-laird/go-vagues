package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"vagues-go/src/backpack"
	"vagues-go/src/trading"

	"github.com/joho/godotenv"
)

func main() {
	// Load .env file
	if err := loadEnvFile(); err != nil {
		log.Printf("警告: 加载 .env 文件失败: %v (将使用系统环境变量或默认值)", err)
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("收到停止信号，正在关闭...")
		cancel()
	}()

	// Create Backpack client from environment variables
	client, err := backpack.NewClientFromEnv()
	if err != nil {
		log.Fatalf("创建Backpack客户端失败: %v", err)
	}
	log.Println("Backpack客户端创建成功")

	// Load trading system configuration from environment variables
	config := loadConfigFromEnv()

	// 检查是否启用多交易对模式
	multiSymbolMode := os.Getenv("MULTI_SYMBOL_MODE")
	if multiSymbolMode == "true" || multiSymbolMode == "1" {
		// 多交易对模式：监控所有 PERP 交易对
		log.Println("=== 启用多交易对监控模式 ===")

		// 创建监控系统
		monitor := trading.NewMultiSymbolMonitor(client, config)

		// 运行交易系统（阻塞直到退出）
		if err := monitor.Run(ctx); err != nil {
			log.Printf("多交易对监控系统运行错误: %v", err)
		}

		// 打印所有交易对的统计信息
		fmt.Println("\n=== 多交易对监控系统性能统计 ===")
		symbols := monitor.GetAllSymbols()
		for _, symbol := range symbols {
			if ts, ok := monitor.GetTradingSystem(symbol); ok {
				performance := ts.GetPerformance()
				if performance.TotalOrders > 0 {
					fmt.Printf("\n--- %s ---\n", symbol)
					fmt.Printf("总订单数: %d\n", performance.TotalOrders)
					fmt.Printf("已平仓订单: %d\n", performance.ClosedOrders)
					fmt.Printf("未平仓订单: %d\n", performance.OpenOrders)
					fmt.Printf("总盈亏: %.4f USDC\n", performance.TotalPnL)
					fmt.Printf("胜率: %.2f%%\n", performance.WinRate)
					fmt.Printf("平均盈利: %.4f USDC\n", performance.AverageWin)
					fmt.Printf("平均亏损: %.4f USDC\n", performance.AverageLoss)
				}
			}
		}
	} else {
		// 单交易对模式：原有的单交易对监控
		log.Println("=== 启用单交易对模式 ===")
		tradingSystem := trading.NewTradingSystem(client, config)

		// Run trading system
		log.Printf("启动交易系统 - 配置: %+v", config)

		if err := tradingSystem.Run(ctx); err != nil {
			log.Printf("交易系统运行错误: %v", err)
		}

		// Print final performance statistics
		performance := tradingSystem.GetPerformance()
		fmt.Println("\n=== 交易系统性能统计 ===")
		fmt.Printf("总订单数: %d\n", performance.TotalOrders)
		fmt.Printf("已平仓订单: %d\n", performance.ClosedOrders)
		fmt.Printf("未平仓订单: %d\n", performance.OpenOrders)
		fmt.Printf("总盈亏: %.4f USDC\n", performance.TotalPnL)
		fmt.Printf("胜率: %.2f%%\n", performance.WinRate)
		fmt.Printf("平均盈利: %.4f USDC\n", performance.AverageWin)
		fmt.Printf("平均亏损: %.4f USDC\n", performance.AverageLoss)
	}

	log.Println("交易系统已停止")
}

// loadEnvFile loads .env file from project root
func loadEnvFile() error {
	// Get project root directory (where go.mod is located)
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("获取工作目录失败: %w", err)
	}

	// Find project root
	rootDir := workDir
	for {
		if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(rootDir)
		if parent == rootDir {
			return fmt.Errorf("未找到项目根目录（包含 go.mod）")
		}
		rootDir = parent
	}

	// Load .env file
	envPath := filepath.Join(rootDir, ".env")
	if err := godotenv.Load(envPath); err != nil {
		// If .env file doesn't exist, it's okay, might use system env vars
		return nil
	}

	return nil
}

// loadConfigFromEnv loads trading configuration from environment variables
func loadConfigFromEnv() trading.Config {
	config := trading.Config{
		// Default values for 1M Pattern + Volume + Delta strategy
		Symbol:           "XPL_USDC_PERP",
		Interval:         "1m",                            // 1 minute timeframe
		Quantity:         0,                               // 不再使用，完全基于账户余额和杠杆动态计算
		StopLossPct:      0.25,                            // 0.25% stop loss (as per spec)
		TakeProfitPct:    0.6,                             // 0.6% take profit (as per spec)
		Leverage:         1,                               // Default to no leverage
		MaxPosPct:        0.02,                            // 2% 最大仓位比例 (as per spec)
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"), // Telegram Bot Token
		TelegramChatID:   os.Getenv("TELEGRAM_CHAT_ID"),   // Telegram Chat ID
	}

	// Load from environment variables
	if symbol := os.Getenv("TRADING_SYMBOL"); symbol != "" {
		config.Symbol = symbol
	}

	if interval := os.Getenv("TRADING_INTERVAL"); interval != "" {
		config.Interval = interval
	}

	// Quantity 不再从环境变量读取，完全基于账户余额和杠杆动态计算

	if leverageStr := os.Getenv("TRADING_LEVERAGE"); leverageStr != "" {
		if leverage, err := strconv.Atoi(leverageStr); err == nil {
			config.Leverage = leverage
		} else {
			log.Printf("警告: 无法解析 TRADING_LEVERAGE=%s, 使用默认值 %d", leverageStr, config.Leverage)
		}
	}

	if maxPosPctStr := os.Getenv("TRADING_MAX_POS_PCT"); maxPosPctStr != "" {
		if maxPosPct, err := strconv.ParseFloat(maxPosPctStr, 64); err == nil {
			config.MaxPosPct = maxPosPct
		} else {
			log.Printf("警告: 无法解析 TRADING_MAX_POS_PCT=%s, 使用默认值 %.2f", maxPosPctStr, config.MaxPosPct)
		}
	}

	// 读取最大监控交易对数量
	if maxSymbolsStr := os.Getenv("MAX_TRADING_SYMBOL"); maxSymbolsStr != "" {
		if maxSymbols, err := strconv.Atoi(maxSymbolsStr); err == nil && maxSymbols > 0 {
			config.MaxTradingSymbols = maxSymbols
		} else {
			log.Printf("警告: 无法解析 MAX_TRADING_SYMBOL=%s, 使用默认值 %d", maxSymbolsStr, config.MaxTradingSymbols)
		}
	} else {
		// 默认值为20
		config.MaxTradingSymbols = 20
	}

	return config
}
