package backpack

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	// BaseURL Backpack API 基础地址
	BaseURL = "https://api.backpack.exchange"
	// DefaultWindow 默认时间窗口（毫秒）
	DefaultWindow = 5000
	// MaxWindow 最大时间窗口（毫秒）
	MaxWindow = 60000
)

// Client Backpack API 客户端
type Client struct {
	apiKey     string // Base64 编码的公钥
	privateKey ed25519.PrivateKey
	httpClient *http.Client
	baseURL    string
	window     int64 // 时间窗口（毫秒）
}

// loadEnvFile 加载 .env 文件
func loadEnvFile() error {
	// 获取项目根目录
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("获取工作目录失败: %w", err)
	}

	// 查找项目根目录（包含 go.mod 的目录）
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

	// 加载 .env 文件
	envPath := filepath.Join(rootDir, ".env")
	if err := godotenv.Load(envPath); err != nil {
		// 如果 .env 文件不存在，不报错，可能使用系统环境变量
		return nil
	}

	return nil
}

// NewClient 创建新的 Backpack 客户端
// apiKey: Base64 编码的公钥
// privateKeySeed: Base64 编码的私钥种子（32字节）
func NewClient(apiKey, privateKeySeed string) (*Client, error) {
	seed, err := base64.StdEncoding.DecodeString(privateKeySeed)
	if err != nil {
		return nil, fmt.Errorf("解码私钥种子失败: %w", err)
	}

	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("私钥种子长度错误，应为 %d 字节", ed25519.SeedSize)
	}

	privateKey := ed25519.NewKeyFromSeed(seed)

	return &Client{
		apiKey:     apiKey,
		privateKey: privateKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: BaseURL,
		window:  DefaultWindow,
	}, nil
}

// NewClientFromEnv 从 .env 文件创建新的 Backpack 客户端
// 需要 .env 文件中包含以下变量：
//   - BACKPACK_API_KEY: Base64 编码的公钥
//   - BACKPACK_PRIVATE_KEY: Base64 编码的私钥种子（32字节）
func NewClientFromEnv() (*Client, error) {
	// 加载 .env 文件
	if err := loadEnvFile(); err != nil {
		return nil, fmt.Errorf("加载 .env 文件失败: %w", err)
	}

	// 从环境变量读取配置
	apiKey := os.Getenv("BACKPACK_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("未找到 BACKPACK_API_KEY，请在 .env 文件中设置或使用环境变量")
	}

	privateKeySeed := os.Getenv("BACKPACK_PRIVATE_KEY")
	if privateKeySeed == "" {
		return nil, fmt.Errorf("未找到 BACKPACK_PRIVATE_KEY，请在 .env 文件中设置或使用环境变量")
	}

	return NewClient(apiKey, privateKeySeed)
}

// SetWindow 设置请求时间窗口（毫秒）
func (c *Client) SetWindow(window int64) error {
	if window < 0 || window > MaxWindow {
		return fmt.Errorf("时间窗口必须在 0-%d 毫秒之间", MaxWindow)
	}
	c.window = window
	return nil
}

// signRequest 生成请求签名
// instruction: 指令类型（如 "orderExecute", "orderCancel" 等）
// params: 请求参数（会被按字母序排序）
func (c *Client) signRequest(instruction string, params map[string]string) (string, string, string, error) {
	// 生成时间戳（毫秒）
	timestamp := time.Now().UnixMilli()
	timestampStr := strconv.FormatInt(timestamp, 10)
	windowStr := strconv.FormatInt(c.window, 10)

	// 将参数按字母序排序并转换为查询字符串格式
	var keys []string
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, params[k]))
	}

	// 构建签名字符串：instruction=xxx&参数&timestamp=xxx&window=xxx
	signString := fmt.Sprintf("instruction=%s", instruction)
	if len(parts) > 0 {
		signString += "&" + strings.Join(parts, "&")
	}
	signString += fmt.Sprintf("&timestamp=%s&window=%s", timestampStr, windowStr)

	// 使用 ED25519 签名
	signature := ed25519.Sign(c.privateKey, []byte(signString))
	signatureBase64 := base64.StdEncoding.EncodeToString(signature)

	return timestampStr, windowStr, signatureBase64, nil
}

// doRequest 执行 HTTP 请求
func (c *Client) doRequest(ctx context.Context, method, path, instruction string, body interface{}) ([]byte, error) {
	var reqBody []byte
	var err error
	params := make(map[string]string)

	// 处理请求体或查询参数
	if method == http.MethodGet || method == http.MethodDelete {
		// GET/DELETE 请求：从 URL 中提取查询参数
		reqURL := c.baseURL + path
		parsedURL, err := url.Parse(reqURL)
		if err == nil {
			for k, v := range parsedURL.Query() {
				if len(v) > 0 {
					params[k] = v[0]
				}
			}
		}
	} else {
		// POST/PATCH 请求：从请求体中提取参数
		if body != nil {
			reqBody, err = json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("序列化请求体失败: %w", err)
			}

			// 将请求体转换为参数映射（用于签名）
			var bodyMap map[string]interface{}
			if err := json.Unmarshal(reqBody, &bodyMap); err == nil {
				for k, v := range bodyMap {
					params[k] = fmt.Sprintf("%v", v)
				}
			}
		}
	}

	// 生成签名（如果 instruction 不为空）
	var timestamp, window, signature string
	if instruction != "" {
		var err error
		timestamp, window, signature, err = c.signRequest(instruction, params)
		if err != nil {
			return nil, fmt.Errorf("生成签名失败: %w", err)
		}
	}

	// 构建请求 URL
	reqURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	if method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete {
		req.Header.Set("Content-Type", "application/json")
	}

	// 如果需要认证，设置认证头
	if instruction != "" {
		req.Header.Set("X-Timestamp", timestamp)
		req.Header.Set("X-Window", window)
		req.Header.Set("X-API-Key", c.apiKey)
		req.Header.Set("X-Signature", signature)
	}

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 检查状态码
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API 请求失败: 状态码 %d, 响应: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// KlineResponse K线数据响应
type KlineResponse struct {
	Start       string `json:"start"`       // 开始时间（字符串格式）
	End         string `json:"end"`         // 结束时间（字符串格式）
	Open        string `json:"open"`        // 开盘价
	High        string `json:"high"`        // 最高价
	Low         string `json:"low"`         // 最低价
	Close       string `json:"close"`       // 收盘价
	Volume      string `json:"volume"`      // 成交量
	QuoteVolume string `json:"quoteVolume"` // 成交额
	Trades      string `json:"trades"`      // 交易数量
}

// GetKlines 获取K线数据（公开端点，不需要认证）
// symbol: 交易对符号（如 "SOL_USDC"）
// interval: 时间间隔（如 "1m", "5m", "15m", "1h", "4h", "1d"）
// startTime: 开始时间（UTC 时间戳，秒，可选）
// endTime: 结束时间（UTC 时间戳，秒，可选）
// limit: 返回的K线数量（可选）
func (c *Client) GetKlines(ctx context.Context, symbol, interval string, startTime, endTime *int64, limit *int) ([]KlineResponse, error) {
	// 构建查询参数
	queryParams := url.Values{}
	queryParams.Set("symbol", symbol)
	queryParams.Set("interval", interval)
	if startTime != nil {
		queryParams.Set("startTime", strconv.FormatInt(*startTime, 10))
	}
	if endTime != nil {
		queryParams.Set("endTime", strconv.FormatInt(*endTime, 10))
	}
	if limit != nil {
		queryParams.Set("limit", strconv.Itoa(*limit))
	}

	path := "/api/v1/klines?" + queryParams.Encode()

	// K线是公开端点，不需要认证（instruction 为空）
	respBody, err := c.doRequest(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return nil, err
	}

	var klines []KlineResponse
	if err := json.Unmarshal(respBody, &klines); err != nil {
		respStr := string(respBody)
		if len(respStr) > 500 {
			respStr = respStr[:500]
		}
		return nil, fmt.Errorf("解析K线数据失败: %w (原始响应前500字符: %s)", err, respStr)
	}

	return klines, nil
}

// OrderRequest 订单请求
type OrderRequest struct {
	Symbol                 string `json:"symbol"`                           // 交易对（如 "SOL_USDC_PERP"）
	Side                   string `json:"side"`                             // 方向："Bid"（买入/做多）或 "Ask"（卖出/做空）
	OrderType              string `json:"orderType"`                        // 订单类型："Limit" 或 "Market"
	Price                  string `json:"price,omitempty"`                  // 价格（限价单必需）
	Quantity               string `json:"quantity,omitempty"`               // 数量（按基础资产）
	QuoteQuantity          string `json:"quoteQuantity,omitempty"`          // 数量（按计价资产）
	TimeInForce            string `json:"timeInForce,omitempty"`            // 有效期："GTC", "IOC", "FOK"
	PostOnly               bool   `json:"postOnly,omitempty"`               // 仅挂单
	ReduceOnly             bool   `json:"reduceOnly,omitempty"`             // 仅减仓
	StopLossTriggerPrice   string `json:"stopLossTriggerPrice,omitempty"`   // 止损触发价格
	StopLossLimitPrice     string `json:"stopLossLimitPrice,omitempty"`     // 止损限价（如果设置，止损将是限价单，否则是市价单）
	StopLossTriggerBy      string `json:"stopLossTriggerBy,omitempty"`      // 触发止损的参考价格（"LastPrice", "MarkPrice" 等）
	TakeProfitTriggerPrice string `json:"takeProfitTriggerPrice,omitempty"` // 止盈触发价格
	TakeProfitLimitPrice   string `json:"takeProfitLimitPrice,omitempty"`   // 止盈限价（如果设置，止盈将是限价单，否则是市价单）
	TakeProfitTriggerBy    string `json:"takeProfitTriggerBy,omitempty"`    // 触发止盈的参考价格（"LastPrice", "MarkPrice" 等）
}

// OrderResponse 订单响应
type OrderResponse struct {
	ID            string      `json:"id"`
	ClientID      string      `json:"clientId,omitempty"`
	Symbol        string      `json:"symbol"`
	Side          string      `json:"side"`
	OrderType     string      `json:"orderType"`
	Quantity      string      `json:"quantity"`
	QuoteQuantity string      `json:"quoteQuantity,omitempty"`
	Price         string      `json:"price,omitempty"`
	Status        string      `json:"status"`
	TimeInForce   string      `json:"timeInForce,omitempty"`
	PostOnly      bool        `json:"postOnly,omitempty"`
	ReduceOnly    bool        `json:"reduceOnly,omitempty"`
	CreatedAt     interface{} `json:"createdAt"` // 可能是字符串或数字（时间戳）
}

// APIError API 错误响应
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PlaceOrder 下单（开多/空仓）
func (c *Client) PlaceOrder(ctx context.Context, req OrderRequest) (*OrderResponse, error) {
	path := "/api/v1/order"

	respBody, err := c.doRequest(ctx, http.MethodPost, path, "orderExecute", req)
	if err != nil {
		// 尝试解析错误响应
		var apiErr APIError
		if jsonErr := json.Unmarshal([]byte(err.Error()), &apiErr); jsonErr == nil {
			// 如果成功解析错误，返回更友好的错误信息
			switch apiErr.Code {
			case "INSUFFICIENT_MARGIN":
				return nil, fmt.Errorf("保证金不足: %s。请检查账户余额或减少下单数量", apiErr.Message)
			case "INVALID_ORDER":
				return nil, fmt.Errorf("订单无效: %s", apiErr.Message)
			case "INSUFFICIENT_BALANCE":
				return nil, fmt.Errorf("余额不足: %s。请检查账户余额", apiErr.Message)
			default:
				return nil, fmt.Errorf("下单失败 [%s]: %s", apiErr.Code, apiErr.Message)
			}
		}
		return nil, err
	}

	var orderResp OrderResponse
	if err := json.Unmarshal(respBody, &orderResp); err != nil {
		return nil, fmt.Errorf("解析订单响应失败: %w", err)
	}

	return &orderResp, nil
}

// CancelOrderRequest 取消订单请求
type CancelOrderRequest struct {
	OrderID string `json:"orderId"` // 订单ID
	Symbol  string `json:"symbol"`  // 交易对
}

// CancelOrder 取消订单（平仓）
func (c *Client) CancelOrder(ctx context.Context, orderID, symbol string) error {
	path := "/api/v1/order"
	req := CancelOrderRequest{
		OrderID: orderID,
		Symbol:  symbol,
	}

	_, err := c.doRequest(ctx, http.MethodDelete, path, "orderCancel", req)
	return err
}

// CancelAllOrdersRequest 取消所有订单请求
type CancelAllOrdersRequest struct {
	Symbol string `json:"symbol,omitempty"` // 交易对（可选，不填则取消所有）
}

// CancelAllOrders 取消所有订单
func (c *Client) CancelAllOrders(ctx context.Context, symbol string) error {
	path := "/api/v1/orders"
	req := CancelAllOrdersRequest{}
	if symbol != "" {
		req.Symbol = symbol
	}

	_, err := c.doRequest(ctx, http.MethodDelete, path, "orderCancelAll", req)
	return err
}

// PositionResponse 持仓信息（根据 FuturePositionWithMargin 结构）
type PositionResponse struct {
	Symbol                   string `json:"symbol"`                   // 交易对
	EntryPrice               string `json:"entryPrice"`               // 入场价格
	MarkPrice                string `json:"markPrice"`                // 标记价格
	NetQuantity              string `json:"netQuantity"`              // 持仓数量（正数=多仓，负数=空仓）
	PositionSize             string `json:"positionSize,omitempty"`   // 兼容字段，使用 netQuantity
	UnrealizedPnl            string `json:"pnlUnrealized"`            // 未实现盈亏
	LiquidationPrice         string `json:"estLiquidationPrice"`      // 预估强平价格
	BreakEvenPrice           string `json:"breakEvenPrice"`           // 盈亏平衡价格
	PnlRealized              string `json:"pnlRealized"`              // 已实现盈亏
	CumulativeFundingPayment string `json:"cumulativeFundingPayment"` // 累计资金费率支付
	NetCost                  string `json:"netCost"`                  // 净成本（正数=多仓，负数=空仓）
	Leverage                 string `json:"leverage,omitempty"`       // 杠杆（可能需要计算）
}

// GetPositions 获取持仓信息
// 根据 API 文档：https://docs.backpack.exchange/#tag/Futures/operation/get_positions
// 正确的端点是 /api/v1/position (单数，不是复数)
// symbol: 可选，指定交易对，如果不指定则返回所有持仓
func (c *Client) GetPositions(ctx context.Context, symbol ...string) ([]PositionResponse, error) {
	// 使用正确的路径：/api/v1/position (单数)
	path := "/api/v1/position"

	// 如果指定了交易对，添加到查询参数
	if len(symbol) > 0 && symbol[0] != "" {
		path = path + "?symbol=" + url.QueryEscape(symbol[0])
	}

	// 使用正确的 instruction: positionQuery
	respBody, err := c.doRequest(ctx, http.MethodGet, path, "positionQuery", nil)
	if err != nil {
		// 如果是 404 错误，可能表示没有持仓，返回空数组
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			return []PositionResponse{}, nil
		}
		return nil, err
	}

	// 如果响应为空，直接返回空数组
	if len(respBody) == 0 {
		return []PositionResponse{}, nil
	}

	var positions []PositionResponse
	if err := json.Unmarshal(respBody, &positions); err != nil {
		// 输出原始响应用于调试
		respStr := string(respBody)
		if len(respStr) > 1000 {
			respStr = respStr[:1000] + "..."
		}
		return nil, fmt.Errorf("解析持仓信息失败: %w (原始响应: %s)", err, respStr)
	}

	// 填充兼容字段 PositionSize（使用 netQuantity）
	for i := range positions {
		if positions[i].PositionSize == "" {
			positions[i].PositionSize = positions[i].NetQuantity
		}
	}

	return positions, nil
}

// ClosePosition 平仓指定交易对的持仓
// 如果有多仓，下空单平仓；如果有空仓，下多单平仓
func (c *Client) ClosePosition(ctx context.Context, symbol string) (*OrderResponse, error) {
	// 1. 获取当前持仓
	positions, err := c.GetPositions(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	// 2. 查找指定交易对的持仓
	var position *PositionResponse
	for i := range positions {
		if positions[i].Symbol == symbol {
			position = &positions[i]
			break
		}
	}

	if position == nil {
		return nil, fmt.Errorf("未找到交易对 %s 的持仓", symbol)
	}

	// 3. 解析持仓数量（优先使用 netQuantity，如果没有则使用 PositionSize）
	positionSizeStr := position.NetQuantity
	if positionSizeStr == "" {
		positionSizeStr = position.PositionSize
	}
	positionSize, err := strconv.ParseFloat(positionSizeStr, 64)
	if err != nil {
		return nil, fmt.Errorf("解析持仓数量失败: %w", err)
	}

	// 如果持仓数量为0或接近0，无需平仓
	if positionSize == 0 || (positionSize > -0.00000001 && positionSize < 0.00000001) {
		return nil, fmt.Errorf("交易对 %s 的持仓数量为 0，无需平仓", symbol)
	}

	// 4. 确定平仓方向
	// 如果持仓数量为正（多仓），需要下空单平仓
	// 如果持仓数量为负（空仓），需要下多单平仓
	var side string
	if positionSize > 0 {
		side = "Ask" // 下空单平多仓
	} else {
		side = "Bid"                 // 下多单平空仓
		positionSize = -positionSize // 转为正数
	}

	// 5. 下平仓订单（使用 ReduceOnly 标志）
	orderReq := OrderRequest{
		Symbol:      symbol,
		Side:        side,
		OrderType:   "Market",                          // 使用市价单快速平仓
		Quantity:    fmt.Sprintf("%.8f", positionSize), // 平仓数量等于持仓数量
		ReduceOnly:  true,                              // 仅减仓标志
		TimeInForce: "IOC",                             // 立即成交或取消
	}

	return c.PlaceOrder(ctx, orderReq)
}

// Balance 余额信息
type Balance struct {
	Asset     string `json:"asset"`
	Free      string `json:"free"`      // 可用余额（兼容字段，实际使用 Available）
	Available string `json:"available"` // 可用余额
	Locked    string `json:"locked"`    // 锁定余额
	Staked    string `json:"staked"`    // 质押余额
}

// balanceDetail 余额详情（用于解析 API 响应）
type balanceDetail struct {
	Available string `json:"available"`
	Locked    string `json:"locked"`
	Staked    string `json:"staked"`
}

// GetBalances 获取余额信息
// 根据 API 文档：https://docs.backpack.exchange/#tag/Capital/operation/get_balances
func (c *Client) GetBalances(ctx context.Context) ([]Balance, error) {
	path := "/api/v1/capital"

	respBody, err := c.doRequest(ctx, http.MethodGet, path, "balanceQuery", nil)
	if err != nil {
		return nil, err
	}

	// API 返回格式为对象：{"ASSET": {"available":"...","locked":"...","staked":"..."}, ...}
	var balanceMap map[string]balanceDetail
	if err := json.Unmarshal(respBody, &balanceMap); err != nil {
		return nil, fmt.Errorf("解析余额信息失败: %w (原始响应: %s)", err, string(respBody))
	}

	// 转换为 Balance 数组
	var balances []Balance
	for asset, detail := range balanceMap {
		balances = append(balances, Balance{
			Asset:     asset,
			Free:      detail.Available, // 兼容字段
			Available: detail.Available,
			Locked:    detail.Locked,
			Staked:    detail.Staked,
		})
	}

	return balances, nil
}

// AccountInfo 账户信息
type AccountInfo struct {
	AutoBorrowSettlements bool   `json:"autoBorrowSettlements"` // 自动借入结算
	AutoLend              bool   `json:"autoLend"`              // 自动借贷
	AutoRealizePnl        bool   `json:"autoRealizePnl"`        // 自动实现盈亏
	AutoRepayBorrows      bool   `json:"autoRepayBorrows"`      // 自动偿还借款
	BorrowLimit           string `json:"borrowLimit"`           // 借款限额
	FuturesMakerFee       string `json:"futuresMakerFee"`       // 期货做市商手续费
	FuturesTakerFee       string `json:"futuresTakerFee"`       // 期货接受者手续费
	LeverageLimit         string `json:"leverageLimit"`         // 杠杆限制（字符串格式）
	LimitOrders           int    `json:"limitOrders"`           // 限价单数量
	Liquidating           bool   `json:"liquidating"`           // 是否清算中
	PositionLimit         string `json:"positionLimit"`         // 持仓限额
	SpotMakerFee          string `json:"spotMakerFee"`          // 现货做市商手续费
	SpotTakerFee          string `json:"spotTakerFee"`          // 现货接受者手续费
	TriggerOrders         int    `json:"triggerOrders"`         // 触发单数量
}

// GetAccount 获取账户信息
// 根据 API 文档：https://docs.backpack.exchange/#tag/Account/operation/get_account
func (c *Client) GetAccount(ctx context.Context) (*AccountInfo, error) {
	path := "/api/v1/account"

	respBody, err := c.doRequest(ctx, http.MethodGet, path, "accountQuery", nil)
	if err != nil {
		return nil, err
	}

	var account AccountInfo
	if err := json.Unmarshal(respBody, &account); err != nil {
		return nil, fmt.Errorf("解析账户信息失败: %w (原始响应: %s)", err, string(respBody))
	}

	return &account, nil
}

// UpdateAccountRequest 更新账户请求（用于设置杠杆等）
type UpdateAccountRequest struct {
	LeverageLimit string `json:"leverageLimit,omitempty"` // 杠杆限制（字符串格式，如 "10"）
}

// UpdateAccount 更新账户设置（设置杠杆）
// 根据 API 文档：https://docs.backpack.exchange/#tag/Account/operation/update_account_settings
func (c *Client) UpdateAccount(ctx context.Context, req UpdateAccountRequest) error {
	path := "/api/v1/account"

	// 使用 accountUpdate 指令
	_, err := c.doRequest(ctx, http.MethodPatch, path, "accountUpdate", req)
	return err
}

// SetLeverage 设置杠杆倍数
func (c *Client) SetLeverage(ctx context.Context, leverage int) error {
	req := UpdateAccountRequest{
		LeverageLimit: strconv.Itoa(leverage),
	}
	return c.UpdateAccount(ctx, req)
}
