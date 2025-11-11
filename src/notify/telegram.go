package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TelegramNotifier Telegram 通知器
type TelegramNotifier struct {
	botToken string
	chatID   string
	client   *http.Client
	enabled  bool
}

// NewTelegramNotifier 创建新的 Telegram 通知器
func NewTelegramNotifier(botToken, chatID string) *TelegramNotifier {
	enabled := botToken != "" && chatID != ""
	return &TelegramNotifier{
		botToken: botToken,
		chatID:   chatID,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		enabled: enabled,
	}
}

// SendMessage 发送文本消息
func (tn *TelegramNotifier) SendMessage(text string) error {
	if !tn.enabled {
		return nil // 如果未启用，静默返回
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", tn.botToken)

	payload := map[string]interface{}{
		"chat_id":    tn.chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化消息失败: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := tn.client.Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Telegram API 错误: 状态码 %d, 响应: %s", resp.StatusCode, string(body))
	}

	return nil
}

// SendOrderNotification 发送订单通知
func (tn *TelegramNotifier) SendOrderNotification(orderType, symbol, quantity, price, stopLoss, takeProfit, orderID string) error {
	emoji := "📈"
	if orderType == "SHORT" || orderType == "开空" {
		emoji = "📉"
	}

	message := fmt.Sprintf(
		"%s <b>%s</b>\n\n"+
			"交易对: <code>%s</code>\n"+
			"数量: <code>%s</code>\n"+
			"价格: <code>%s</code>\n"+
			"止损: <code>%s</code>\n"+
			"止盈: <code>%s</code>\n"+
			"订单ID: <code>%s</code>",
		emoji, orderType, symbol, quantity, price, stopLoss, takeProfit, orderID,
	)

	return tn.SendMessage(message)
}

// SendCloseNotification 发送平仓通知
func (tn *TelegramNotifier) SendCloseNotification(symbol, quantity, exitPrice, pnl, pnlPercent, orderID string) error {
	emoji := "✅"
	if pnl != "" && len(pnl) > 0 {
		// 尝试判断盈亏
		if pnl[0] == '-' {
			emoji = "❌"
		}
	}

	message := fmt.Sprintf(
		"%s <b>平仓通知</b>\n\n"+
			"交易对: <code>%s</code>\n"+
			"数量: <code>%s</code>\n"+
			"平仓价: <code>%s</code>\n"+
			"盈亏: <code>%s</code>\n"+
			"盈亏%%: <code>%s</code>\n"+
			"订单ID: <code>%s</code>",
		emoji, symbol, quantity, exitPrice, pnl, pnlPercent, orderID,
	)

	return tn.SendMessage(message)
}

// SendErrorNotification 发送错误通知
func (tn *TelegramNotifier) SendErrorNotification(title, message string) error {
	text := fmt.Sprintf("⚠️ <b>%s</b>\n\n%s", title, message)
	return tn.SendMessage(text)
}
