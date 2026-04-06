package loop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/state"
)

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
	ThinkingContext string
	Content         string
	Error           error
	SummaryText     string
	PendingTool     *[]funcs.ToolCall
	StopReason      StopReason
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
	sendQueue    chan msgObj
	recvQueue    chan AIResponse
	lock         sync.Mutex
	isResponding bool
	cancelFunc   context.CancelFunc
	ctxCancel    context.CancelFunc
	session      *structs.Chats
	ctx          context.Context
}

const queueSize = 100

// New 创建一个新的循环对象
func New(session *structs.Chats) *Object {
	return &Object{
		sendQueue: make(chan msgObj, queueSize),
		recvQueue: make(chan AIResponse, queueSize),
		lock:      sync.Mutex{},
		session:   session,
	}

}

// Start 启动 Demo Loop
func (p *Object) Start(ctx context.Context) {
	var cancel context.CancelFunc
	p.ctx, cancel = context.WithCancel(ctx)
	p.ctxCancel = cancel
	defer cancel()
	p.session.Context = &p.ctx

	session := p.session
	call := func(resp AIResponse) {
		p.recvQueue <- resp
	}

	var runResponseLoop func()
	runResponseLoop = func() {
		// 启动 loop
		loopCount := 0
		for {
			thinkingFlag := false
			responseStarted := false

			responseCtx, responseCancel := context.WithCancel(p.ctx)
			p.lock.Lock()
			p.isResponding = true
			p.cancelFunc = responseCancel
			p.lock.Unlock()

			finish, err := funcs.SendRequest(responseCtx, session, func(delta string, thinkingDelta string) error {
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
					call(AIResponse{
						ThinkingContext: thinkingDelta,
						Content:         delta,
					})
				}
				return nil
			})

			p.lock.Lock()
			p.isResponding = false
			p.cancelFunc = nil
			p.lock.Unlock()

			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					break
				}
				call(AIResponse{
					Error:      fmt.Errorf("loop error in request: %v", err),
					StopReason: StopReasonError,
				})
				break
			}

			if finish {
				if session.State == state.StateWaitApprove {
					autoHandled, approved, pendingTools, pErr := funcs.AutoHandlePendingToolCalls(session)
					if pErr != nil {
						call(AIResponse{
							Error:      fmt.Errorf("loop error in pending tool calls: %v", pErr),
							StopReason: StopReasonError,
						})
					} else if autoHandled {
						if approved {
							runResponseLoop()
							return
						}
						break
					} else if len(pendingTools) > 0 {
						call(AIResponse{
							PendingTool: &pendingTools,
							StopReason:  StopReasonError,
						})
					}
					break
				}
				if !responseStarted && !thinkingFlag {
					call(AIResponse{
						Error:      errors.New("no response"),
						StopReason: StopReasonError,
					})
				}
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
		call(AIResponse{
			StopReason: StopReasonModel,
		})
	}

	// 启动时如有待审批，尝试自动处理并提示用户
	if session.State == state.StateWaitApprove {
		autoHandled, approved, pendingTools, err := funcs.AutoHandlePendingToolCalls(session)
		if err != nil {
			call(AIResponse{
				Error:      fmt.Errorf("loop error in pending tool calls: %v", err),
				StopReason: StopReasonError,
			})
		} else if autoHandled {
			if approved {
				func() {
					runResponseLoop()
				}()
			}
		} else if len(pendingTools) > 0 {
			call(AIResponse{
				PendingTool: &pendingTools,
				StopReason:  StopReasonPendingTool,
			})
		}
	}

	// 获取用户输入
	for {
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
			summaryText, err := funcs.SummarySession(p.ctx, session)
			if err != nil {
				call(AIResponse{
					Error:      fmt.Errorf("loop error when summary %v", err),
					StopReason: StopReasonError,
				})
			}

			call(AIResponse{
				SummaryText: summaryText,
				StopReason:  StopReasonUser,
			})
		case msgActionApprove:
			err := funcs.ApproveToolCalls(session)
			if err != nil {
				call(AIResponse{
					Error:      fmt.Errorf("loop error when approve %v", err),
					StopReason: StopReasonUser,
				})
			}

			// 显示 AI 响应
			runResponseLoop()
		default:
			input = strings.TrimSpace(input)

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

			// 显示 AI 响应
			runResponseLoop()
		}
	}
}

// Stop 停止当前消息的生成
func (p *Object) Stop() {
	p.lock.Lock()
	cancel := p.cancelFunc
	p.lock.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Cancel 终止 Loop，遵从上层 context
func (p *Object) Cancel() {
	if p.ctxCancel != nil {
		p.ctxCancel()
	}
}

// Chat 发送消息
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

// ChangeModel 切换模型
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

// Summary 获取摘要
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

// Approve 审批
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

// SetCallback 设置回调
func (p *Object) SetCallback(callFunc func(AIResponse)) {
	go func() {
		for {
			select {
			case call := <-p.recvQueue:
				callFunc(call)
			default:
				if p.ctx != nil {
					select {
					case <-p.ctx.Done():
						return
					default:
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
}
