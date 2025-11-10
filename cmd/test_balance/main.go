package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"vagues-go/src/backpack"

	"github.com/joho/godotenv"
)

func main() {
	// 加载 .env 文件（从项目根目录）
	// 尝试多个可能的路径
	envPaths := []string{
		".env",                            // 当前目录
		"../.env",                         // 上一级目录
		"../../.env",                      // 上两级目录
		filepath.Join("..", "..", ".env"), // 项目根目录
	}

	var envLoaded bool
	for _, envPath := range envPaths {
		if err := godotenv.Load(envPath); err == nil {
			log.Printf("✅ 成功加载 .env 文件: %s", envPath)
			envLoaded = true
			break
		}
	}

	if !envLoaded {
		log.Printf("⚠️  警告: 未找到 .env 文件，将使用系统环境变量")
	}

	// 创建上下文
	ctx := context.Background()

	// 创建 Backpack 客户端
	client, err := backpack.NewClientFromEnv()
	if err != nil {
		log.Fatalf("创建Backpack客户端失败: %v", err)
	}
	log.Println("✅ Backpack客户端创建成功")

	// 测试获取余额
	log.Println("\n=== 开始测试 GetBalances ===")
	balances, err := client.GetBalances(ctx)
	if err != nil {
		log.Fatalf("❌ 获取余额失败: %v", err)
	}

	// 打印结果
	log.Println("\n=== 余额获取结果 ===")
	if len(balances) == 0 {
		log.Println("⚠️  账户余额列表为空")
		os.Exit(0)
	}

	log.Printf("共找到 %d 种资产:\n", len(balances))
	for i, balance := range balances {
		available, _ := parseFloat(balance.Available)
		locked, _ := parseFloat(balance.Locked)
		staked, _ := parseFloat(balance.Staked)
		total := available + locked + staked

		log.Printf("\n[%d] 资产: %s", i+1, balance.Asset)
		log.Printf("    可用: %s (%.4f)", balance.Available, available)
		log.Printf("    锁定: %s (%.4f)", balance.Locked, locked)
		log.Printf("    质押: %s (%.4f)", balance.Staked, staked)
		log.Printf("    总计: %.4f", total)

		// 特别标记 USD 和 USDC
		if balance.Asset == "USD" || balance.Asset == "USDC" || balance.Asset == "USDT" {
			if total > 0 {
				log.Printf("    ⭐ 这是稳定币，余额 > 0")
			} else {
				log.Printf("    ⚠️  这是稳定币，但余额为 0")
			}
		}
	}

	// 查找 USD/USDC
	log.Println("\n=== 查找 USD/USDC 余额 ===")
	foundUSD := false
	foundUSDC := false
	var usdBalance, usdcBalance *backpack.Balance

	for i := range balances {
		if balances[i].Asset == "USD" {
			foundUSD = true
			usdBalance = &balances[i]
		}
		if balances[i].Asset == "USDC" {
			foundUSDC = true
			usdcBalance = &balances[i]
		}
	}

	if foundUSD {
		available, _ := parseFloat(usdBalance.Available)
		locked, _ := parseFloat(usdBalance.Locked)
		staked, _ := parseFloat(usdBalance.Staked)
		total := available + locked + staked
		log.Printf("✅ 找到 USD: 可用=%.4f, 锁定=%.4f, 质押=%.4f, 总计=%.4f",
			available, locked, staked, total)
	} else {
		log.Println("❌ 未找到 USD")
	}

	if foundUSDC {
		available, _ := parseFloat(usdcBalance.Available)
		locked, _ := parseFloat(usdcBalance.Locked)
		staked, _ := parseFloat(usdcBalance.Staked)
		total := available + locked + staked
		log.Printf("✅ 找到 USDC: 可用=%.4f, 锁定=%.4f, 质押=%.4f, 总计=%.4f",
			available, locked, staked, total)
	} else {
		log.Println("❌ 未找到 USDC")
	}

	// 推荐使用的资产
	log.Println("\n=== 推荐使用的资产 ===")
	if foundUSD {
		available, _ := parseFloat(usdBalance.Available)
		if available > 0 {
			log.Printf("✅ 推荐使用 USD (可用余额: %.4f)", available)
		}
	} else if foundUSDC {
		available, _ := parseFloat(usdcBalance.Available)
		if available > 0 {
			log.Printf("✅ 推荐使用 USDC (可用余额: %.4f)", available)
		}
	} else {
		log.Println("⚠️  未找到 USD 或 USDC，请检查账户余额")
	}

	log.Println("\n=== 测试完成 ===")
}

// parseFloat 辅助函数，安全地解析浮点数
func parseFloat(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}
