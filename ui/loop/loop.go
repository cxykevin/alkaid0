package loop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/log"
	reqStructs "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/state"
)

// 重试配置
const (
	// maxRetries 请求失败时的最大重试次数
	maxRetries = 3
	// baseBackoff 指数退避的基准间隔（秒），第 n 次重试等待 baseBackoff * 2^(n-1)
	baseBackoff = 1 * time.Second
)

var logger = log.New("loop")

// StopReason 停止原因
type StopReason uint8

const (
	// StopReasonNone 无
	StopReasonNone StopReason = iota
	// StopReasonModel 模型自行停止
	StopReasonModel
	// StopReasonUser 用户停止
	StopReasonUser
	// StopReasonError 错误
	StopReasonError
	// StopReasonPendingTool 等待工具调用
	StopReasonPendingTool
)

// AIResponse AI 响应
type AIResponse struct {
	MsgID           uint64
	ThinkingContext string
	Content         string
	Error           error
	SummaryText     string
	PendingTool     *[]funcs.ToolCall
	StopReason      StopReason
	ToolCallContent map[string]any
	Usage           *reqStructs.Usage
	SummaryFlag     bool
	AgentID         *string
}

// msgAction 停止原因
type msgAction uint8

const (
	// msgActionNone 无
	msgActionNone msgAction = iota
	// msgActionSummary 摘要
	msgActionSummary
	msgActionApprove
)

type msgObj struct {
	Msg     string
	Refers  []any
	Command msgAction
}

// Object 循环对象
type Object struct {
	sendQueue      chan msgObj
	recvQueue      chan AIResponse
	recvSyncQueue  chan struct{}
	lock           sync.Mutex
	isResponding   bool
	cancelFunc     context.CancelFunc // 取消当前 LLM 请求
	toolCancelFunc context.CancelFunc // 取消正在执行中的工具（ExecuteToolCalls）
	ctxCancel      context.CancelFunc // 取消整个循环生命周期
	session        *structs.Chats
	ctx            context.Context
	done           chan struct{}
	closeOnce      sync.Once
}

// queueSize 队列缓冲区大小
const queueSize = 100

// New 创建一个新的循环对象
func New(session *structs.Chats) *Object {
	return &Object{
		sendQueue:     make(chan msgObj, queueSize),
		recvQueue:     make(chan AIResponse, queueSize),
		recvSyncQueue: make(chan struct{}),
		lock:          sync.Mutex{},
		session:       session,
		done:          make(chan struct{}),
	}

}

// Start 启动 Demo Loop。主事件循环处理 AI 请求/响应的完整生命周期：
//
//	Idle → (用户输入) → Requesting → Reciving → WaitApprove
//	                                           ↓ (auto-reject)
//	                                           Idle ← (reject/approve)
//
// needCompress 标志在 token 累积超过模型配置的压缩阈值后触发自动摘要
func (p *Object) Start(ctx context.Context) {
	logger.Info("start loop in session %d", p.session.ID)

	// 确保无论 Start() 如何退出都关闭 done 通道，
	// 避免 SetCallback 的 goroutine 泄漏。
	defer p.closeOnce.Do(func() {
		close(p.done)
	})

	var cancel context.CancelFunc
	p.ctx, cancel = context.WithCancel(ctx)
	p.ctxCancel = cancel
	defer cancel()
	p.session.SetContext(p.ctx)

	session := p.session
	call := func(resp AIResponse) {
		select {
		case p.recvQueue <- resp:
		case <-p.done:
			return
		}
		select {
		case <-p.recvSyncQueue:
		case <-p.done:
		}
	}

	var needCompress bool

	// doAutoSummary 执行自动摘要并发送回调通知（提取的公共逻辑，复用 4 次）
	doAutoSummary := func() {
		logger.Info("start auto summary in session=%d", session.ID)
		call(AIResponse{SummaryText: "", SummaryFlag: true})
		summaryText, err := funcs.SummarySession(p.ctx, session)
		if err != nil {
			call(AIResponse{Error: fmt.Errorf("loop error when auto summary %v", err), StopReason: StopReasonError})
		}
		call(AIResponse{SummaryText: summaryText, SummaryFlag: true})
		needCompress = false
	}

	// runResponseLoop 启动响应循环：发送请求 → 接收流式响应 → 处理工具调用结果 → 判断是否需要继续
	var runResponseLoop func()
	runResponseLoop = func() {
		loopCount := 0
		for {
			thinkingFlag := false
			responseStarted := false

			// 为每次请求创建独立的可取消 context
			responseCtx, responseCancel := context.WithCancel(p.ctx)
			p.lock.Lock()
			p.isResponding = true
			p.cancelFunc = responseCancel
			p.lock.Unlock()

			// 令牌数达到压缩阈值时，在下一轮请求前执行自动摘要
			if needCompress {
				doAutoSummary()
			}

			// sendRequestWithRetry 执行请求，失败时根据配置重试（指数退避）
			var sendRequestWithRetry func() (bool, error)
			sendRequestWithRetry = func() (bool, error) {
				// 重置每轮重试的状态
				thinkingFlag = false
				responseStarted = false

				finish, err := funcs.SendRequest(responseCtx, session, func(delta string, thinkingDelta string, id uint64, usage reqStructs.Usage, agentID *string) error {
					select {
					case <-responseCtx.Done():
						return responseCtx.Err()
					default:
					}
					if thinkingDelta != "" {
						if !thinkingFlag {
							thinkingFlag = true
						}
					}

					if delta != "" {
						if thinkingFlag {
							thinkingFlag = false
						}
						if !responseStarted {
							responseStarted = true
						}
					}
					call(AIResponse{
						MsgID:           id,
						ThinkingContext: thinkingDelta,
						Content:         delta,
						Usage:           &usage,
						AgentID:         agentID,
					})

					if usage.TotalTokens != 0 {
						// get modelID
						modelID := session.LastModelID
						if session.CurrentAgentID != "" {
							modelIDRet := uint32(session.CurrentAgentConfig.AgentModel)
							if modelIDRet != 0 {
								modelID = modelIDRet
							}
						}
						modelCfg, ok := config.GlobalConfig.Model.Models[int32(modelID)]
						if ok {
							if modelCfg.CompressSize != 0 && usage.TotalTokens >= modelCfg.CompressSize {
								needCompress = true
							}
							if modelCfg.CompressSize == 0 && usage.TotalTokens >= 100000 {
								needCompress = true
							}
						}

					}
					return nil
				})
				return finish, err
			}

			finish, err := sendRequestWithRetry()

			// 重试逻辑：仅在未开始流式响应且非用户取消的错误时重试
			if err != nil && !responseStarted {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					// 用户主动取消，不重试
					logger.Info("request canceled by user, skip retry")
				} else {
					for retryCount := 1; retryCount <= maxRetries; retryCount++ {
						backoff := baseBackoff * (1 << (retryCount - 1)) // 指数退避: 1s, 2s, 4s
						logger.Warn("request failed (attempt %d/%d), retrying in %v: %v",
							retryCount, maxRetries, backoff, err)

						select {
						case <-time.After(backoff):
						case <-p.ctx.Done():
							goto retryDone
						case <-responseCtx.Done():
							goto retryDone
						}

						finish, err = sendRequestWithRetry()
						if err == nil || responseStarted {
							break
						}
						if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
							break
						}
					}
				}
			}
		retryDone:

			p.lock.Lock()
			p.isResponding = false
			p.cancelFunc = nil
			p.lock.Unlock()

			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					call(AIResponse{
						StopReason: StopReasonUser,
					})
					break
				}
				call(AIResponse{
					Error:      fmt.Errorf("loop error in request: %v", err),
					StopReason: StopReasonError,
				})
				break
			}

			// finish=true 表示 LLM 已完成当前轮响应
			if finish {
				// 若 LLM 发出了工具调用，状态会变为 WaitApprove
				// 等待用户或自动规则决策后才能继续
				if session.State == state.StateWaitApprove {
					// autoHandle 处理逻辑：
					//   优先级：拒绝规则 > 手动审批 > 自动审批规则
					//   autoHandled=true 表示规则做出了决策
					//   approved=true 表示规则允许执行
					restoreToolCtx := p.runWithToolCancel(session)
				autoHandled, approved, pendingTools, msgID, pErr := funcs.AutoHandlePendingToolCalls(session)
				restoreToolCtx()
					if pErr != nil {
						call(AIResponse{
							Error:      fmt.Errorf("loop error in pending tool calls: %v", pErr),
							StopReason: StopReasonError,
						})
						break
					} else if autoHandled {
						if approved {
							continue
						}

						if session.CurrentAgentID != "" {
							funcs.SubAgentReject(session)
							continue
						}

						if needCompress {
							doAutoSummary()
						}

						call(AIResponse{
							StopReason: StopReasonModel,
						})
					} else if len(pendingTools) > 0 {
						if session.CurrentAgentID != "" {
							funcs.SubAgentReject(session)
							continue
						}
						call(AIResponse{
							MsgID:       msgID,
							PendingTool: &pendingTools,
							StopReason:  StopReasonPendingTool,
						})
						break
					}
					if session.CurrentAgentID != "" {
						funcs.SubAgentReject(session)
						continue
					}

					if needCompress {
						doAutoSummary()
					}
					call(AIResponse{
						StopReason: StopReasonModel,
					})
					break
				}
				if !responseStarted && !thinkingFlag {
					call(AIResponse{
						Error:      errors.New("no response"),
						StopReason: StopReasonError,
					})
					break
				}
				call(AIResponse{
					StopReason: StopReasonModel,
				})
				break
			}

			loopCount++
			if loopCount >= int(config.GlobalConfig.Agent.MaxCallCount) {
				call(AIResponse{
					Error:      fmt.Errorf("loop count exceeded %d", config.GlobalConfig.Agent.MaxCallCount),
					StopReason: StopReasonError,
				})
				break
			}
		}
	}

	// 处理启动时的待审批状态：从数据库恢复的会话可能有未完成的工具调用
	if session.State == state.StateWaitApprove {
		logger.Info("waiting approve in session=%d", session.ID)
		session.ToolState = 1
		restoreToolCtx := p.runWithToolCancel(session)
		autoHandled, approved, pendingTools, msgID, err := funcs.AutoHandlePendingToolCalls(session)
		restoreToolCtx()
		if err != nil {
			session.ToolState = 0
			call(AIResponse{
				Error:      fmt.Errorf("loop error in pending tool calls: %v", err),
				StopReason: StopReasonError,
			})
		} else if autoHandled {
			if approved {
				call(AIResponse{
					MsgID:           msgID,
					ThinkingContext: "",
					Content:         "",
				})
				func() {
					runResponseLoop()
				}()
			}
			session.ToolState = 0
		} else if len(pendingTools) > 0 {
			session.ToolState = 0
			call(AIResponse{
				MsgID:       msgID,
				PendingTool: &pendingTools,
				StopReason:  StopReasonPendingTool,
			})
		}
		session.ToolState = 0
	}

	// 获取用户输入
	for {
		if needCompress {
			doAutoSummary()
		}
		select {
		case <-p.ctx.Done():
			call(AIResponse{
				StopReason: StopReasonUser,
			})
			return
		default:
		}
		var input string
		var callObj msgObj

		select {
		case callObj = <-p.sendQueue:
			input = callObj.Msg
		case <-p.ctx.Done():
			call(AIResponse{
				StopReason: StopReasonUser,
			})
			return
		}
		switch callObj.Command {
		case msgActionSummary:
			logger.Info("start summary in session=%d", session.ID)
			call(AIResponse{
				SummaryText: "",
				SummaryFlag: true,
			})
			summaryText, err := funcs.SummarySession(p.ctx, session)
			if err != nil {
				call(AIResponse{
					Error:      fmt.Errorf("loop error when summary %v", err),
					StopReason: StopReasonError,
				})
			}

			call(AIResponse{
				SummaryText: summaryText,
				SummaryFlag: true,
			})

			call(AIResponse{
				StopReason: StopReasonUser,
			})
		case msgActionApprove:
			session.ToolState = 1
			logger.Info("approve tools in session=%d", session.ID)
			restoreToolCtx := p.runWithToolCancel(session)
			msgID, err := funcs.ApproveToolCalls(session)
			restoreToolCtx()
			if err != nil {
				call(AIResponse{
					Error:      fmt.Errorf("loop error when approve %v", err),
					StopReason: StopReasonUser,
				})
			}
			session.CurrentMessageID = msgID
			call(AIResponse{
				MsgID:           msgID,
				ThinkingContext: "",
				Content:         "",
			})
			session.ToolState = 0

			session.LatestToolCallingContext = make(map[string]any)
			session.LatestToolCallingType = make(map[string]string)

			// 显示 AI 响应
			runResponseLoop()
		default:
			input = strings.TrimSpace(input)
			logger.Info("run in session=%d with input \"%s\"", session.ID, input)

			if input == "" {
				continue
			}

			// 处理特殊命令
			if input == "!" {
				input = ""
			} else {
				err := funcs.UserAddMsg(session, input, nil)
				if err != nil {
					call(AIResponse{
						Error:      fmt.Errorf("loop error when calling %v", err),
						StopReason: StopReasonError,
					})
				}
			}

			session.LatestToolCallingContext = make(map[string]any)
			session.LatestToolCallingType = make(map[string]string)

			// 显示 AI 响应
			runResponseLoop()
		}
	}
}

// Stop 停止当前正在进行的操作。
//   - LLM 请求阶段：取消 responseCtx，终止流式响应。
//   - 工具执行阶段：
//     1. 取消 toolCancelFunc，通过 session.GetContext() 通知 runCmd 等 kill 进程。
//     2. 调用 session.KillTool()，调用工具注册的直接停止回调（如 kill 进程）。
//   - 空闲阶段：无操作（nothing to stop）。
func (p *Object) Stop() {
	p.lock.Lock()
	cancel := p.cancelFunc
	toolCancel := p.toolCancelFunc
	p.lock.Unlock()
	if cancel != nil {
		cancel()
	}
	if toolCancel != nil {
		toolCancel()
	}
	// 调用工具注册的直接停止函数（所有工具通用的中断入口）
	if p.session != nil {
		p.session.KillTool()
	}
}

// Cancel 终止整个 Loop 生命周期（而非仅当前请求）。
// 调用后 Start() 主循环退出，所有等待中的消息被丢弃。
func (p *Object) Cancel() {
	p.closeOnce.Do(func() {
		close(p.done)
	})
	if p.ctxCancel != nil {
		p.ctxCancel()
	}
}

// runWithToolCancel 在工具执行期间设置可取消的上下文，使 Stop() 能够中断正在执行的工具。
// 返回 restore 函数，调用后恢复原始 session 上下文并清理 toolCancelFunc。
// 使用方式：defer p.runWithToolCancel(session)()
func (p *Object) runWithToolCancel(session *structs.Chats) func() {
	toolCtx, toolCancel := context.WithCancel(p.ctx)
	p.lock.Lock()
	p.toolCancelFunc = toolCancel
	p.lock.Unlock()
	oldCtx := session.GetContext()
	session.SetContext(toolCtx)
	return func() {
		p.lock.Lock()
		p.toolCancelFunc = nil
		p.lock.Unlock()
		toolCancel()
		session.SetContext(oldCtx)
	}
}

// Chat 将用户消息发送到循环的处理队列。队列满时返回错误而非阻塞。
// refers 参数用于消息引用（前端指定上下文片段）。
func (p *Object) Chat(msg string, refers []any) error {
	obj := msgObj{
		Msg:    msg,
		Refers: refers,
	}
	select {
	case p.sendQueue <- obj:
		return nil
	default:
		return fmt.Errorf("send msg error: send queue full")
	}
}

// ChangeModel 切换当前会话使用的 AI 模型。
// 先验证模型 ID 有效性，再更新会话记录中的模型选择。
func (p *Object) ChangeModel(modelID int) error {
	_, err := funcs.GetModelInfo(int32(modelID))
	if err != nil {
		return fmt.Errorf("change model error: %v", err)
	}
	err = funcs.SelectModel(p.session, int32(modelID))
	if err != nil {
		return fmt.Errorf("change model error: %v", err)
	}
	return nil
}

// Summary 请求对当前对话进行摘要压缩，发送 msgActionSummary 指令到处理队列。
// 用于在 token 数接近模型限制时清理上下文。
func (p *Object) Summary() error {
	obj := msgObj{
		Command: msgActionSummary,
	}
	select {
	case p.sendQueue <- obj:
		return nil
	default:
		return fmt.Errorf("summary error: send queue full")
	}
}

// Approve 审批待处理的工具调用，发送 msgActionApprove 指令到处理队列。
// 用户确认后执行工具调用，然后将结果继续发给 LLM。
func (p *Object) Approve() error {
	obj := msgObj{
		Command: msgActionApprove,
	}
	select {
	case p.sendQueue <- obj:
		return nil
	default:
		return fmt.Errorf("approve error: send queue full")
	}
}

// SetCallback 注册 AI 响应回调函数。在独立 goroutine 中循环读取 recvQueue，
// 将 AIResponse 对象传递给 UI 层进行处理。通过 recvSyncQueue 实现背压同步。
//
// 使用双 chan select 而非 busy-poll + sleep，idle 时 goroutine 挂起不消耗 CPU。
func (p *Object) SetCallback(callFunc func(AIResponse)) {
	go func() {
		for {
			select {
			case call := <-p.recvQueue:
				callFunc(call)
				p.recvSyncQueue <- struct{}{} // 通知发送方已完成处理
			case <-p.done:
				return
			}
		}
	}()
}
