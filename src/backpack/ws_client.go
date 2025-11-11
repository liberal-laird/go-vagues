package backpack

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// WSBaseURL WebSocket 基础地址
	WSBaseURL = "wss://api.backpack.exchange"
	// WSReconnectInterval WebSocket 重连间隔
	WSReconnectInterval = 5 * time.Second
	// WSPingInterval WebSocket Ping 间隔
	WSPingInterval = 30 * time.Second
	// WSReadTimeout WebSocket 读取超时
	WSReadTimeout = 60 * time.Second
	// WSWriteTimeout WebSocket 写入超时
	WSWriteTimeout = 10 * time.Second
)

// WSClient WebSocket 客户端
type WSClient struct {
	apiKey        string
	privateKey    ed25519.PrivateKey
	conn          *websocket.Conn
	connMutex     sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	subscribed    map[string]bool // 已订阅的交易对
	subMutex      sync.RWMutex
	handlers      map[string]func([]byte) // 消息处理器
	handlersMutex sync.RWMutex
	reconnectChan chan struct{}
}

// WSMessage WebSocket 消息
type WSMessage struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     int             `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *WSError        `json:"error,omitempty"`
}

// WSError WebSocket 错误
type WSError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// WSKlineMessage K线数据消息
type WSKlineMessage struct {
	Symbol   string    `json:"symbol"`
	Interval string    `json:"interval"`
	Kline    KlineData `json:"kline"`
}

// KlineData K线数据
type KlineData struct {
	Start       string `json:"start"`
	End         string `json:"end"`
	Open        string `json:"open"`
	High        string `json:"high"`
	Low         string `json:"low"`
	Close       string `json:"close"`
	Volume      string `json:"volume"`
	QuoteVolume string `json:"quoteVolume"`
	Trades      string `json:"trades"`
}

// NewWSClient 创建新的 WebSocket 客户端
func NewWSClient(apiKey, privateKeySeed string) (*WSClient, error) {
	// 解析私钥
	privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeySeed)
	if err != nil {
		return nil, fmt.Errorf("解码私钥种子失败: %w", err)
	}

	if len(privateKeyBytes) != 32 {
		return nil, fmt.Errorf("私钥种子长度必须为32字节")
	}

	privateKey := ed25519.NewKeyFromSeed(privateKeyBytes)

	ctx, cancel := context.WithCancel(context.Background())

	return &WSClient{
		apiKey:        apiKey,
		privateKey:    privateKey,
		ctx:           ctx,
		cancel:        cancel,
		subscribed:    make(map[string]bool),
		handlers:      make(map[string]func([]byte)),
		reconnectChan: make(chan struct{}, 1),
	}, nil
}

// NewWSClientFromEnv 从环境变量创建 WebSocket 客户端
func NewWSClientFromEnv() (*WSClient, error) {
	apiKey := os.Getenv("BACKPACK_API_KEY")
	privateKeySeed := os.Getenv("BACKPACK_PRIVATE_KEY")

	if apiKey == "" || privateKeySeed == "" {
		return nil, fmt.Errorf("BACKPACK_API_KEY 和 BACKPACK_PRIVATE_KEY 环境变量必须设置")
	}

	return NewWSClient(apiKey, privateKeySeed)
}

// Connect 连接到 WebSocket 服务器
func (ws *WSClient) Connect(ctx context.Context) error {
	ws.connMutex.Lock()
	defer ws.connMutex.Unlock()

	if ws.conn != nil {
		return fmt.Errorf("WebSocket 已连接")
	}

	// 构建 WebSocket URL
	wsURL := WSBaseURL + "/ws"

	// 创建 WebSocket 连接
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("连接 WebSocket 失败: %w", err)
	}

	ws.conn = conn

	// 启动消息处理 goroutine
	go ws.readLoop()
	go ws.pingLoop()

	return nil
}

// Disconnect 断开 WebSocket 连接
func (ws *WSClient) Disconnect() error {
	ws.cancel()

	ws.connMutex.Lock()
	defer ws.connMutex.Unlock()

	if ws.conn != nil {
		err := ws.conn.Close()
		ws.conn = nil
		return err
	}

	return nil
}

// SubscribeKlines 订阅 K线数据
func (ws *WSClient) SubscribeKlines(symbol, interval string, handler func(WSKlineMessage)) error {
	stream := fmt.Sprintf("%s@kline_%s", symbol, interval)

	ws.subMutex.Lock()
	if ws.subscribed[stream] {
		ws.subMutex.Unlock()
		return fmt.Errorf("已订阅: %s", stream)
	}
	ws.subscribed[stream] = true
	ws.subMutex.Unlock()

	// 构建订阅消息
	subscribeMsg := WSMessage{
		Method: "SUBSCRIBE",
		Params: json.RawMessage(fmt.Sprintf(`["%s"]`, stream)),
		ID:     int(time.Now().Unix()),
	}

	// 发送订阅消息
	if err := ws.sendMessage(subscribeMsg); err != nil {
		ws.subMutex.Lock()
		delete(ws.subscribed, stream)
		ws.subMutex.Unlock()
		return fmt.Errorf("发送订阅消息失败: %w", err)
	}

	// 注册消息处理器
	ws.handlersMutex.Lock()
	ws.handlers[stream] = func(data []byte) {
		var klineMsg WSKlineMessage
		if err := json.Unmarshal(data, &klineMsg); err == nil {
			handler(klineMsg)
		}
	}
	ws.handlersMutex.Unlock()

	return nil
}

// UnsubscribeKlines 取消订阅 K线数据
func (ws *WSClient) UnsubscribeKlines(symbol, interval string) error {
	stream := fmt.Sprintf("%s@kline_%s", symbol, interval)

	ws.subMutex.Lock()
	if !ws.subscribed[stream] {
		ws.subMutex.Unlock()
		return fmt.Errorf("未订阅: %s", stream)
	}
	delete(ws.subscribed, stream)
	ws.subMutex.Unlock()

	// 构建取消订阅消息
	unsubscribeMsg := WSMessage{
		Method: "UNSUBSCRIBE",
		Params: json.RawMessage(fmt.Sprintf(`["%s"]`, stream)),
		ID:     int(time.Now().Unix()),
	}

	// 发送取消订阅消息
	if err := ws.sendMessage(unsubscribeMsg); err != nil {
		return fmt.Errorf("发送取消订阅消息失败: %w", err)
	}

	// 移除消息处理器
	ws.handlersMutex.Lock()
	delete(ws.handlers, stream)
	ws.handlersMutex.Unlock()

	return nil
}

// sendMessage 发送 WebSocket 消息
func (ws *WSClient) sendMessage(msg WSMessage) error {
	ws.connMutex.RLock()
	conn := ws.conn
	ws.connMutex.RUnlock()

	if conn == nil {
		return fmt.Errorf("WebSocket 未连接")
	}

	// 设置写入超时
	conn.SetWriteDeadline(time.Now().Add(WSWriteTimeout))

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化消息失败: %w", err)
	}

	return conn.WriteMessage(websocket.TextMessage, data)
}

// readLoop 读取消息循环
func (ws *WSClient) readLoop() {
	for {
		select {
		case <-ws.ctx.Done():
			return
		default:
			ws.connMutex.RLock()
			conn := ws.conn
			ws.connMutex.RUnlock()

			if conn == nil {
				return
			}

			// 设置读取超时
			conn.SetReadDeadline(time.Now().Add(WSReadTimeout))

			_, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket 读取错误: %v", err)
					// 触发重连
					select {
					case ws.reconnectChan <- struct{}{}:
					default:
					}
				}
				return
			}

			// 处理消息
			ws.handleMessage(data)
		}
	}
}

// pingLoop Ping 循环
func (ws *WSClient) pingLoop() {
	ticker := time.NewTicker(WSPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ws.ctx.Done():
			return
		case <-ticker.C:
			ws.connMutex.RLock()
			conn := ws.conn
			ws.connMutex.RUnlock()

			if conn != nil {
				conn.SetWriteDeadline(time.Now().Add(WSWriteTimeout))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					log.Printf("WebSocket Ping 失败: %v", err)
				}
			}
		}
	}
}

// handleMessage 处理接收到的消息
func (ws *WSClient) handleMessage(data []byte) {
	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Printf("解析 WebSocket 消息失败: %v, 数据: %s", err, string(data))
		return
	}

	// 处理订阅响应
	if msg.Result != nil {
		// 订阅成功，不需要特殊处理
		return
	}

	// 处理错误
	if msg.Error != nil {
		log.Printf("WebSocket 错误: %s (代码: %d)", msg.Error.Message, msg.Error.Code)
		return
	}

	// 处理 K线数据推送
	// Backpack WebSocket 消息格式可能不同，需要根据实际 API 调整
	// 这里假设消息格式为: {"stream": "symbol@kline_interval", "data": {...}}
	var streamMsg struct {
		Stream string          `json:"stream"`
		Data   json.RawMessage `json:"data"`
	}

	if err := json.Unmarshal(data, &streamMsg); err == nil && streamMsg.Stream != "" {
		ws.handlersMutex.RLock()
		handler, ok := ws.handlers[streamMsg.Stream]
		ws.handlersMutex.RUnlock()

		if ok && handler != nil {
			handler(streamMsg.Data)
		}
	}
}

// IsConnected 检查是否已连接
func (ws *WSClient) IsConnected() bool {
	ws.connMutex.RLock()
	defer ws.connMutex.RUnlock()
	return ws.conn != nil
}
