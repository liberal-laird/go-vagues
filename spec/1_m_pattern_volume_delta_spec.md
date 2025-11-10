# 1M — Pattern + Volume + Delta 短线策略 Spec

## 版本与作者
- 版本：v1.0
- 作者：由 ChatGPT 生成（可作为开发/回测参考）
- 目标：为1分钟级别短线交易提供一份完整、可回测、可实现的策略规范（spec.md）。

---

## 概要
基于 1 分钟 K 线（1M）结合：**价格形态（pattern）**、**成交量（volume）**、**买卖吃单差（delta / order flow）**，构建进场过滤与确认逻辑，目标是提升信号质量并在噪音极高的短周期内取得正期望。包含进出场规则、风控、回测指标、参数建议、实现细节及 Go 实现要点。

---

## 设计原则
1. **多信号过滤**：只有当 pattern、volume、delta 三者一致时才进场，显著降低噪音。  
2. **风险优先**：严格止损、限制仓位与并发交易数量，保证回撤可控。  
3. **可回测与可重复**：所有参数可配置，所有交易与中间数据持久化用于审计与优化。  
4. **程序化执行**：1M 策略高度依赖执行速度与数据质量，推荐自动化并在低延迟环境中运行。 

---

## 策略总体流程（高层）
1. 订阅交易所逐笔 trade（带时间、价格、数量），可选深度快照（order book）。  
2. 将逐笔 trade 聚合成 1m candles；每根 candle 结束时触发策略评估。  
3. 在 candle 结束时运行：pattern 检测 → volume 判断 → delta 计算 → 趋势过滤（可选） → 进场/下单逻辑。  
4. 持仓中用止损/止盈/追踪止损/超时退出管理仓位。  
5. 记录所有日志、订单与回测指标。 

---

## 数据需求
- **必须**：逐笔 trade（timestamp, price, size, aggressor side 如果有）；1m candle（open, high, low, close, volume）。  
- **强烈建议**：order book 快照或增量（L2/L3），以便更精确判断 aggressor side 与潜在滑点。  
- **历史数据长度**：最低 6 个月（建议 12 个月以上），包含不同波动阶段（牛市、震荡、空头）。  
- **质量**：无缺失、时间戳一致、对齐到毫秒或秒级。 

---

## 信号组件详细定义

### 1) Pattern（形态检测）
支持的 pattern（每个 pattern 返回方向与置信度）：
- **Engulfing（吞没）**：当前 K 的实体完全包住上一根 K 且方向相反。返回方向 = 当前 K 的方向（LONG/SHORT）。
- **Hammer / Inverted Hammer（锤子/倒锤）**：实体短且下影或上影 >= 实体 * H_RATIO（H_RATIO 默认为 2.0）。
- **Inside Bar（内包）**：当前 K 的 high < prev.high 且 low > prev.low（用于等待突破情景）。
- **Breakout（突破）**：收盘价突破过去 `B_PLOOKBACK` 根高/低（如 5 根）并伴随放量。方向由突破方向决定。
- **Momentum Candle（动量烛）**：实体占 K 总长度 ≥ M_RATIO（例如 0.7），并伴随显著放量。

实现细节：每个 pattern 函数应返回 `{direction: LONG|SHORT|NONE, confidence: 0..1, name}`。confidence 可在后续权重评估或日志中使用。

---

### 2) Volume（成交量过滤）
- `V_LOOKBACK`：用于计算基准平均量的窗口（默认 20 根 1m candles）。
- 条件：`candle.volume >= avg(volume over V_LOOKBACK) * V_MULT`。默认 `V_MULT = 1.25`。
- 可选加强：只在市场波动率（例如 ATR）高于阈值时应用该过滤，避免在极低波动期触发假信号。

---

### 3) Delta（买卖吃单差 / order flow）
- **定义**：在一个指定短窗口内（可为当前1m或过去若干 ticks），将 aggressor buy volume 与 aggressor sell volume 相减：
  `delta = sum(buy_aggressor_volume) - sum(sell_aggressor_volume)`。
- **aggressor 判定**：若 trade.price >= bestAsk（当前卖一价）则判为买方吃单；若 trade.price <= bestBid 则判为卖方吃单。若无法得到 book，则用成交价与上一 tick 的买卖方向推断（不如直接 book 精准）。
- **窗口**：`DELTA_LOOKBACK_TICKS`（例如 40 ticks）或 `DELTA_LOOKBACK_SECONDS`（例如 3-5 秒）或直接用当前 1m 内累加。
- **动态阈值**：`DELTA_THRESH` 建议动态计算：`DELTA_THRESH = mean_abs_delta_last_N * D_MULT`（例如 `D_MULT = 0.8`），或设为绝对值（合约手数）。
- **判断**：对于 LONG 信号要求 `delta >= DELTA_THRESH`；SHORT 要求 `delta <= -DELTA_THRESH`。

---

### 4) 趋势过滤（可选）
- 使用长期 EMA（例如 EMA_LONG=55）判断大趋势方向，若启用必须与 pattern 方向一致才允许进场，降低逆势开仓的比例。
- 可选额外条件：只在价格位于 EMA_LONG 之上允许多头，位于 EMA_LONG 之下允许空头。

---

## 进场规则（并发/大小/限价策略）

### 触发条件（必须同时满足）
1. `pattern.direction != NONE`。  
2. `vol_ok`：candle.volume >= avg(V_LOOKBACK) * V_MULT。  
3. `delta_ok`：delta 与 pattern 方向一致并超过阈值。  
4. `trend_ok`（如果启用）：EMA_long 与 pattern 方向一致。

若上述都满足：
- 计算仓位 `size = calc_position_size(account_equity, MAX_POS_PCT, stop_loss_distance)`。
- 优先挂 **limit maker** 单（提交 maker 价降低手续费），并设置短超时（e.g. 1s-3s）：若未成交且信号仍强可改用 IOC/市价吃单。
- 若成交为部分成交，允许小幅补单但不得超过 `MAX_POS_PCT`。

---

## 出场规则
1. **止盈**：达到 `TAKE_PROFIT_PCT`（默认 0.6%）→ 平仓全部或部分。  
2. **止损**：达到 `STOP_LOSS_PCT`（默认 0.25%）→ 立即平仓全部。  
3. **触及半 TP**：当未完全到 TP，但已实现 TP 的 50% 时，启用追踪止损（`TRAIL_PCT`，默认 0.2%）。  
4. **超时退出**：持仓超过 `MAX_HOLD_BARS`（例如 12 根 1m = 12 分钟）且未触及 TP/SL，则平仓或改为更紧的 trail。  

---

## 风控与仓位管理
- `MAX_POS_PCT`：单笔最大仓位占总权益比例（默认 2%）。
- `MAX_CONCURRENT_TRADES`：并行持仓数（建议 1）。
- `MAX_DAILY_TRADES`：可选限制，防止在极端高频日子过度交易。  
- `MAX_DRAWDOWN_STOP`：若账户净值回撤超过阈值（例如 12%），自动暂停交易并发送告警。  
- 每笔交易记录 entry_price、stop_loss_price、size、order_ids、entry_delta、entry_volume、pattern_name。  

---

## 参数清单（默认值）
```json
{
  "timeframe": "1m",
  "ema_short": 8,
  "ema_long": 55,
  "v_lookback": 20,
  "v_mult": 1.25,
  "delta_lookback_ticks": 40,
  "delta_thresh_mode": "dynamic", // dynamic 或 absolute
  "delta_thresh_abs": 100, // 若用绝对值
  "delta_dyn_mult": 0.8,
  "stop_loss_pct": 0.0025,
  "take_profit_pct": 0.006,
  "trailing_enabled": true,
  "trailing_pct": 0.002,
  "max_pos_pct": 0.02,
  "max_concurrent_trades": 1,
  "fee": 0.0004,
  "slippage": 0.0008,
  "max_hold_bars": 12
}
```

---

## 伪代码（核心）
```python
on_new_candle(candle):
  pattern = detect_patterns(candle, prev_candles)
  if pattern.direction == NONE: return

  avg_vol = sma(volume_array[-V_LOOKBACK:])
  vol_ok = candle.volume >= avg_vol * V_MULT

  delta = calc_delta_recent(DELTA_LOOKBACK_TICKS)
  delta_ok = (pattern.direction == LONG and delta >= DELTA_THRESH) or
             (pattern.direction == SHORT and delta <= -DELTA_THRESH)

  trend_ok = True
  if USE_TREND_FILTER:
    trend_ok = ema(EMA_LONG).direction == pattern.direction

  if vol_ok and delta_ok and trend_ok and can_open_more_positions():
    size = calc_size(account_equity, MAX_POS_PCT, candle, stop_loss_pct)
    order_id = place_limit_maker_or_ioc(side=pattern.direction, size=size)
    record_entry(order_id, ...)

on_trade_update(order):
  handle_partial_and_full_fills()

on_position_update(pos):
  if price_hit(TAKE_PROFIT): close_position()
  if price_hit(STOP_LOSS): close_position()
  if held_too_long: close_or_trail()
```

---

## 回测策略与评估

### 必要回测步骤
1. 使用逐笔 trade 生成 1m candles（或使用历史1m但需保证 delta 可得）。  
2. 回测需包含手续费与滑点模型（可分两部分：静态滑点 + 成交量相关滑点）。  
3. 参数网格搜索（grid search）与敏感性分析（哪些参数对结果影响最大）。  
4. 声明随机性：用 Monte Carlo 随机抖动 entry/exit 时间和滑点，评估稳健性。  

### 必报指标
- 总收益 / 年化收益  
- 胜率（win%）  
- 平均盈利 / 平均亏损  
- 盈亏比 (avg win / avg loss)  
- 最大回撤（绝对与%）  
- 夏普比率、Sortino（如需）  
- 日均交易数、月均交易数  
- 含手续费/滑点的净利润  
- 单笔交易分布（boxplot）  

---

## 典型回测预期（仅经验值，需在你数据上验证）
- 胜率：45%–62%（受 DELTA_THRESH 与 V_MULT 调整影响大）
- 平均盈亏比：1.4–2.2
- 日均交易数：50–300（合约和波动性相关）
- 手续滑点影响：可吞噬净收益 10–25%

---

## 实现细节（Go 语言优先建议）

### 架构建议
- **数据层**：WebSocket 客户端接收 trade 与 book，持久化到本地时序 DB 或 Redis（用于回放）。
- **聚合层**：goroutine 聚合 tick → 1m candles，保证时间对齐；使用 channels 将 candle 发送到策略模块。
- **策略层**：纯计算模块（pattern detection、delta calc、indicator），无 IO。便于单元测试。  
- **执行层**：下单模块独立，负责限价挂单、IOC、重试、持久化 order 状态。  
- **风控/监控**：独立服务或 goroutine 检查总仓位、当天 PnL、回撤并发送告警。  

### 技术细节
- 精确数值：使用 decimal 库（`shopspring/decimal`）避免浮点误差。  
- 并发安全：状态（positions/orders）通过 channel 或加锁结构统一管理，避免竞态。  
- 日志：结构化日志（JSON），每笔 trade/order/candle 写入文件或 DB，便于回放。  
- 单元测试：pattern detection/ delta calc / position sizing 应有独立测试。  

---

## 参数优化建议
- 先对 `V_MULT` 与 `DELTA_THRESH` 做粗粒度网格搜索（例如 V_MULT: 1.0,1.1,1.25,1.5；DELTA_MULT:0.6,0.8,1.0,1.2）。
- 固定手续费/滑点模型后，对 `STOP_LOSS_PCT` 与 `TAKE_PROFIT_PCT` 做二次搜索以优化盈亏比。
- 对于“动态 threshold”使用历史 std/mean 统计替代固定阈值，增强稳健性。
- 做 walk-forward 验证（滚动回测），避免过拟合。 

---

## 监控与告警建议
- 实时监控：未完成订单数、未成交 maker 单比例、平均下单延迟、今日净 PnL、仓位占比。  
- 告警触发：网络断开、订阅断开、回测阈值被触发（如日回撤超过阈值）、异常大单。  
- 日志与快照：每日结束保存当日交易快照，供事后复盘。 

---

## 常见陷阱与注意事项
1. **数据质量差** → delta 计算错误 → 信号误判。  
2. **忽视手续费/滑点** → 回测显著优于实盘。  
3. **过度参数化** → 导致过拟合。  
4. **手工下单** → 在 1M 场景下不稳定，建议自动化。  
5. **做市/流动性变化** → 不同时间段（周末/夜盘）需场景区分策略或禁用。 

---

## 输出日志字段（建议 schema）
```
timestamp, symbol, candle_time, entry_price, entry_size, entry_side,
entry_pattern, entry_pattern_confidence, entry_volume, entry_delta,
exit_price, exit_time, exit_reason, pnl_before_fees, pnl_after_fees,
max_unrealized_drawdown, fees_paid, slippage_paid, order_ids
```

---

## 下一步（可交付项）
- 我可以把本 spec 转为 **Go 代码 skeleton**，包含：
  - `main.go`（初始化/配置/运行 loop）
  - `data_agg.go`（tick->1m 聚合）
  - `strategy.go`（pattern/volume/delta 检测）
  - `executor.go`（下单/订单管理/重试）
  - `backtest_runner.go`（可注入历史数据回测）

- 或者我可以直接生成一个 **可运行的回测脚本（Go）**，用于你本地导入历史 trade 数据进行回测。

---

## 许可
- 本文档为参考规范，可用于研究与软件开发。请在使用前根据目标市场与合约单位调整参数。

---

*结束*
