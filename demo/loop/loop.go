package loop

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/state"
	"gorm.io/gorm"
)

const (
	// ColorReset 重置颜色
	ColorReset = "\033[0m"
	// ColorRed 红色
	ColorRed = "\033[31m"
	// ColorGreen 绿色
	ColorGreen = "\033[32m"
	// ColorYellow 黄色
	ColorYellow = "\033[33m"
	// ColorBlue 蓝色
	ColorBlue = "\033[34m"
	// ColorPurple 紫色
	ColorPurple = "\033[35m"
	// ColorCyan 青色
	ColorCyan = "\033[36m"
	// ColorWhite 白色
	ColorWhite = "\033[37m"
	// ColorBold 加粗
	ColorBold = "\033[1m"
)

var logger *log.LogsObj

func init() {
	logger = log.New("loop")
}

func assert(err error) {
	if err != nil {
		panic(err)
	}
}

func unwrap[T any](args T, err error) T {
	if err != nil {
		panic(err)
	}
	return args
}

// printBoxHeader 打印简单的盒子标题
func printBoxHeader(title string, color string) {
	fmt.Printf("\n%s%s┌─ %s ─┐%s\n", ColorBold, color, title, ColorReset)
}

// Start 启动 Demo Loop
func Start(ctx context.Context, db *gorm.DB) {
	fmt.Println("\033[2J")
	logger.Info("loop initing")
	reader := bufio.NewReader(os.Stdin)

	chats := unwrap(funcs.GetChats(db))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGINT)
	defer signal.Stop(sigCh)

	var isResponding bool
	var cancelResponse context.CancelFunc
	var mu sync.Mutex

	interruptCh := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-sigCh:
				mu.Lock()
				responding := isResponding
				cancel := cancelResponse
				mu.Unlock()
				if responding {
					if cancel != nil {
						cancel()
					}
					select {
					case interruptCh <- struct{}{}:
					default:
					}
				} else {
					fmt.Printf("\n%s%sGoodbye!%s\n", ColorBold, ColorCyan, ColorReset)
					os.Exit(0)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// 显示标题
	fmt.Printf("%s%s╔════════════════════════════════════════════════════════════╗%s\n", ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s║%s  Chat Sessions Manager                                     %s%s║%s\n", ColorBold, ColorCyan, ColorReset, ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s╚════════════════════════════════════════════════════════════╝%s\n", ColorBold, ColorCyan, ColorReset)

	showChatList := func() {
		if len(chats) == 0 {
			fmt.Printf("%s%sNo existing chats found.%s\n", ColorYellow, ColorBold, ColorReset)
		} else {
			fmt.Printf("\n%s%sAvailable Chat Sessions:%s\n", ColorBold, ColorBlue, ColorReset)
			for idx, v := range chats {
				fmt.Printf("  %s[%2d]%s Chat #%d\n", ColorGreen, idx+1, ColorReset, v.ID)
				logger.Debug("(chats)discover chat %d,%v", idx+1, v.ID)
			}
		}

		fmt.Printf("\n%sCommands:%s\n", ColorBold, ColorReset)
		fmt.Printf("  %s[ 1-%d]%s %sEnter existing chat (N = chat number)%s\n", ColorGreen, len(chats), ColorReset, ColorBlue, ColorReset)
		fmt.Printf("  %s[   0]%s %sCreate new chat%s\n", ColorGreen, ColorReset, ColorBlue, ColorReset)
		if len(chats) > 0 {
			fmt.Printf("  %s[  -N]%s %sDelete chat (N = chat number)%s\n", ColorRed, ColorReset, ColorBlue, ColorReset)
		}
	}

	showChatList()

	var input string
	chatNum := 0
	flag := true
	for flag {
		fmt.Printf("\n%s%s┌─ Command ─┐%s\n", ColorBold, ColorPurple, ColorReset)
		fmt.Printf("%s%s│ DO>%s ", ColorBold, ColorPurple, ColorReset)
		input = unwrap(reader.ReadString('\n'))
		input = strings.TrimSpace(input)
		logger.Debug("user input: %v", input)

		inputNum, err := strconv.Atoi(input)
		if err != nil {
			fmt.Printf("%s❌ Invalid input: please enter a number%s\n", ColorRed, ColorReset)
			continue
		}

		if inputNum < 0 {
			// 删除聊天
			absNum := -inputNum
			if absNum < 1 || absNum > len(chats) {
				fmt.Printf("%s❌ Invalid chat number: %d (valid range: 1-%d)%s\n", ColorRed, absNum, len(chats), ColorReset)
				continue
			}
			deletedChat := chats[absNum-1]
			logger.Info("delete chat %d (ID: %v)", absNum, deletedChat.ID)

			assert(funcs.DeleteChat(db, &deletedChat))
			chats = unwrap(funcs.GetChats(db))

			fmt.Printf("%s✓ Chat #%d deleted successfully%s\n", ColorGreen, deletedChat.ID, ColorReset)
			showChatList()
		} else {
			// 创建或进入聊天
			if inputNum == 0 {
				logger.Info("create new chat")
				newID := unwrap(funcs.CreateChat(db))
				chats = unwrap(funcs.GetChats(db))
				inputNum = len(chats)
				fmt.Printf("%s✓ New chat created (ID: %d)%s\n", ColorGreen, newID, ColorReset)
			}

			if inputNum < 1 || inputNum > len(chats) {
				fmt.Printf("%s❌ Invalid chat number: %d (valid range: 1-%d)%s\n", ColorRed, inputNum, len(chats), ColorReset)
				continue
			}

			chatNum = inputNum - 1
			flag = false
		}
	}

	sessionObj := unwrap(funcs.InitChat(db, &chats[chatNum]))
	session := *sessionObj
	modelName := funcs.GetModelName(session.LastModelID, "unknown")
	logger.Debug("use chat ID:%v|Agent:%v|Model:%v", session.ID, session.NowAgent, session.LastModelID)

	fmt.Printf("\n%s%s╔════════════════════════════════════════════════════════════╗%s\n", ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s║%s  Chat Session: %s%-44d%s%s%s║%s\n", ColorBold, ColorCyan, ColorReset, ColorYellow, session.ID, ColorReset, ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s╚════════════════════════════════════════════════════════════╝%s\n", ColorBold, ColorCyan, ColorReset)

	// 显示配置信息
	printBoxHeader("Configuration", ColorPurple)

	fmt.Printf("  %sModel:%s  %s\n", ColorBlue, ColorReset, modelName)
	if session.NowAgent != "" {
		fmt.Printf("  %sAgent:%s  %s\n", ColorBlue, ColorReset, session.NowAgent)
	} else {
		fmt.Printf("  %sAgent:%s  %s(none)%s\n", ColorBlue, ColorReset, ColorYellow, ColorReset)
	}

	// 显示历史消息
	fmt.Printf("\n%s%s╔════════════════════════════════════════════════════════════╗%s\n", ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s║%s  Conversation History                                      %s%s║%s\n", ColorBold, ColorCyan, ColorReset, ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s╚════════════════════════════════════════════════════════════╝%s\n", ColorBold, ColorCyan, ColorReset)

	chatMsgs := unwrap(funcs.GetHistory(&session))

	if len(chatMsgs) == 0 {
		fmt.Printf("\n%s%sNo messages yet. Start typing to begin!%s\n", ColorYellow, ColorBold, ColorReset)
	} else {
		for _, v := range chatMsgs {
			logger.Debug("(history)discover history %v", strings.ReplaceAll(fmt.Sprintf("%v", v), "\n", "\\n"))

			switch v.Type {
			case 0: // User
				fmt.Printf("\n%s%s┌─ User ─┐%s\n", ColorBold, ColorGreen, ColorReset)
				if v.ThinkingDelta != "" {
					fmt.Printf("%s[Thinking]%s %s\n", ColorPurple, ColorReset, v.ThinkingDelta)
				}
				fmt.Printf("%s\n", v.Delta)

			case 1: // AI
				modelDisplay := funcs.GetModelName(v.ModelID, "unknown")

				fmt.Printf("\n%s%s┌─ AI (%s%s%s", ColorBold, ColorBlue, ColorReset, modelDisplay, ColorBold+ColorBlue)
				if v.AgentID != nil && *v.AgentID != "" {
					fmt.Printf(" | Agent: %s%s%s", ColorReset, *v.AgentID, ColorBold+ColorBlue)
				}
				fmt.Printf("%s) ─┐%s\n", ColorBold+ColorBlue, ColorReset)

				if v.ThinkingDelta != "" {
					fmt.Printf("%s[Thinking]%s%s\n", ColorBlue, v.ThinkingDelta, ColorReset)
				}
				fmt.Printf("%s\n", v.Delta)

			case 2: // Tool
				fmt.Printf("\n%s%s┌─ Tool ─┐%s\n", ColorBold, ColorYellow, ColorReset)
				fmt.Printf("%s\n", v.Delta)
			}
		}
	}

	fmt.Printf("\n%s%s╔════════════════════════════════════════════════════════════╗%s\n", ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s║%s  Ready for Input                                           %s%s║%s\n", ColorBold, ColorCyan, ColorReset, ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s╚════════════════════════════════════════════════════════════╝%s\n", ColorBold, ColorCyan, ColorReset)

	fmt.Printf("%sType /help for available commands or enter your message:%s\n", ColorBlue, ColorReset)
	var lastInput string

	var runResponseLoop func()
	runResponseLoop = func() {
		// 显示 AI 响应
		fmt.Printf("\n%s%s┌─ AI Response ─┐%s\n", ColorBold, ColorBlue, ColorReset)

		// 启动 loop
		loopCount := 0
		for {
			thinkingFlag := false
			responseStarted := false

			responseCtx, responseCancel := context.WithCancel(context.Background())
			mu.Lock()
			isResponding = true
			cancelResponse = responseCancel
			mu.Unlock()

			finish, err := funcs.SendRequest(responseCtx, &session, func(delta string, thinkingDelta string) error {
				select {
				case <-responseCtx.Done():
					return responseCtx.Err()
				default:
				}
				if thinkingDelta != "" {
					if !thinkingFlag {
						thinkingFlag = true
					}
					fmt.Printf("%s%s%s", ColorBlue, thinkingDelta, ColorReset)
				}

				if delta != "" {
					if thinkingFlag {
						fmt.Printf("\n")
						thinkingFlag = false
					}
					if !responseStarted {
						responseStarted = true
					}
					fmt.Print(delta)
				}
				return nil
			})

			mu.Lock()
			isResponding = false
			cancelResponse = nil
			mu.Unlock()

			if thinkingFlag {
				fmt.Printf("\n")
			}

			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					break
				}
				fmt.Printf("\n%s❌ Error:%s\n%v\n", ColorRed, ColorReset, err)
				break
			}

			if finish {
				if session.State == state.StateWaitApprove {
					autoHandled, approved, pendingTools, pErr := funcs.AutoHandlePendingToolCalls(&session)
					if pErr != nil {
						fmt.Printf("%s❌ Failed to load pending tool calls: %v%s\n", ColorRed, pErr, ColorReset)
					} else if autoHandled {
						if approved {
							runResponseLoop()
							return
						}
						break
					} else if len(pendingTools) > 0 {
						fmt.Printf("\n%s%s┌─ Pending Tool Calls ─┐%s\n", ColorBold, ColorYellow, ColorReset)
						for idx, t := range pendingTools {
							fmt.Printf("%s[%d]%s %s (id=%s)\n", ColorYellow, idx+1, ColorReset, t.Name, t.ID)
						}
						fmt.Printf("%sUse /approve or type anything to continue.%s\n", ColorYellow, ColorReset)
					}
					break
				}
				if !responseStarted && !thinkingFlag {
					fmt.Printf("%s(No response)%s\n", ColorYellow, ColorReset)
				}
				break
			}

			loopCount++
			if loopCount >= int(config.GlobalConfig.Agent.MaxCallCount) {
				fmt.Printf("\n%s(loop count exceeded %d)%s\n", ColorYellow, config.GlobalConfig.Agent.MaxCallCount, ColorReset)
				break
			}
		}
	}

	// 启动时如有待审批，尝试自动处理并提示用户
	if session.State == state.StateWaitApprove {
		autoHandled, approved, pendingTools, err := funcs.AutoHandlePendingToolCalls(&session)
		if err != nil {
			fmt.Printf("%s❌ Failed to load pending tool calls: %v%s\n", ColorRed, err, ColorReset)
		} else if autoHandled {
			if approved {
				func() {
					runResponseLoop()
				}()
			}
		} else if len(pendingTools) > 0 {
			fmt.Printf("\n%s%s┌─ Pending Tool Calls ─┐%s\n", ColorBold, ColorYellow, ColorReset)
			for idx, t := range pendingTools {
				fmt.Printf("%s[%d]%s %s (id=%s)\n", ColorYellow, idx+1, ColorReset, t.Name, t.ID)
			}
			fmt.Printf("%sUse /approve or type anything to continue.%s\n", ColorYellow, ColorReset)
		}
	}

	// 获取用户输入
	for {
		select {
		case <-interruptCh:
			fmt.Printf("\n%s%s(Interrupted)%s\n", ColorBold, ColorYellow, ColorReset)
			mu.Lock()
			isResponding = false
			mu.Unlock()
			continue
		default:
		}
		select {
		case <-ctx.Done():
			fmt.Printf("\n%s%sReceived signal, shutting down...%s\n", ColorBold, ColorYellow, ColorReset)
			return
		default:
		}
		var input string
		fmt.Printf("\n%s%s┌─ Input ─┐%s\n", ColorBold, ColorPurple, ColorReset)
		fmt.Printf("%s%s│ >%s ", ColorBold, ColorPurple, ColorReset)
		input = unwrap(reader.ReadString('\n'))
		input = strings.TrimSpace(input)
		logger.Debug("user input: %v", input)

		if input == "" {
			continue
		}

		// 处理特殊命令
		if input == "!" {
			fmt.Printf("%s%s(Repeating)%s\n", ColorBold, ColorYellow, ColorReset)
			input = lastInput
		} else {
			lastInput = input
		}

		if input[0] == '/' {
			args := strings.Fields(input)
			if len(args) == 0 {
				continue
			}

			switch args[0] {
			case "/help":
				fmt.Printf("\n%s%s╔════════════════════════════════════════════════════════════╗%s\n", ColorBold, ColorCyan, ColorReset)
				fmt.Printf("%s%s║%s  Available Commands                                        %s%s║%s\n", ColorBold, ColorCyan, ColorReset, ColorBold, ColorCyan, ColorReset)
				fmt.Printf("%s%s╚════════════════════════════════════════════════════════════╝%s\n", ColorBold, ColorCyan, ColorReset)
				fmt.Printf("\n%sGeneral Commands:%s\n", ColorBold, ColorReset)
				fmt.Printf("  %s/help%s     Show this help message\n", ColorGreen, ColorReset)
				fmt.Printf("  %s/exit%s     Exit the chat loop\n", ColorGreen, ColorReset)
				fmt.Printf("  %s/models%s   List available models\n", ColorGreen, ColorReset)
				fmt.Printf("  %s/model%s    Set current model (e.g., /model 1)\n", ColorGreen, ColorReset)
				fmt.Printf("  %s/summary%s  Generate summary of conversation\n", ColorGreen, ColorReset)
				fmt.Printf("\n%sAgent Commands:%s\n", ColorBold, ColorReset)
				fmt.Printf("  %s/agent list%s       List all available agents\n", ColorGreen, ColorReset)
				fmt.Printf("  %s/agent used%s       List agents used in this chat\n", ColorGreen, ColorReset)
				fmt.Printf("  %s/agent add ...%s     Add an agent to project\n", ColorGreen, ColorReset)
				fmt.Printf("  %s/agent activate%s   Activate an agent\n", ColorGreen, ColorReset)
				fmt.Printf("  %s/agent deactivate%s Deactivate current agent\n", ColorGreen, ColorReset)
				fmt.Printf("\n%sScope Commands:%s\n", ColorBold, ColorReset)
				fmt.Printf("  %s/scope list%s        List available scopes\n", ColorGreen, ColorReset)
				fmt.Printf("  %s/scope enable%s      Enable a scope\n", ColorGreen, ColorReset)
				fmt.Printf("  %s/scope disable%s     Disable a scope\n", ColorGreen, ColorReset)
				fmt.Printf("\n%sApproval Commands:%s\n", ColorBold, ColorReset)
				fmt.Printf("  %s/approve%s  Approve pending tool calls\n", ColorGreen, ColorReset)
				fmt.Printf("  %s(others)%s  Reject pending tool calls and tell reasons\n", ColorGreen, ColorReset)
				fmt.Printf("\n%sShortcuts:%s\n", ColorBold, ColorReset)
				fmt.Printf("  %s!%s Repeat last input\n", ColorGreen, ColorReset)

			case "/exit":
				fmt.Printf("\n%s%sGoodbye!%s\n", ColorBold, ColorCyan, ColorReset)
				os.Exit(0)

			case "/models":
				printBoxHeader("Available Models", ColorBlue)
				models := funcs.GetModels()
				if len(models) == 0 {
					fmt.Printf("  %sNo models configured%s\n", ColorYellow, ColorReset)
				} else {
					for _, v := range models {
						marker := " "
						if uint32(v.ID) == session.LastModelID {
							marker = "✓"
						}
						fmt.Printf("  %s[%s]%s [%2d] %s%-20s%s (%s)\n",
							ColorGreen, marker, ColorReset, v.ID, ColorBold, v.Config.ModelName, ColorReset, v.Config.ModelID)
					}
				}

			case "/model":
				if len(args) < 2 {
					fmt.Printf("%s❌ Usage: /model <id>%s\n", ColorRed, ColorReset)
					continue
				}
				modelID, err := strconv.Atoi(args[1])
				if err != nil {
					fmt.Printf("%s❌ Invalid model ID: must be a number%s\n", ColorRed, ColorReset)
					continue
				}
				modelInfo, err := funcs.GetModelInfo(int32(modelID))
				if err != nil {
					fmt.Printf("%s❌ Model not found: %d%s\n", ColorRed, modelID, ColorReset)
					continue
				}
				assert(funcs.SelectModel(&session, int32(modelID)))
				fmt.Printf("%s✓ Model changed to: %s%s%s\n", ColorGreen, ColorBold, modelInfo.ModelName, ColorReset)

			case "/approve":
				if session.State != state.StateWaitApprove {
					fmt.Printf("%sNo pending tool calls%s\n", ColorYellow, ColorReset)
					continue
				}
				if err := funcs.ApproveToolCalls(&session); err != nil {
					fmt.Printf("%s❌ Approve failed: %v%s\n", ColorRed, err, ColorReset)
				} else {
					fmt.Printf("%s✓ Tool calls approved%s\n", ColorGreen, ColorReset)
					runResponseLoop()
				}
				continue

			case "/agent":
				if len(args) < 2 {
					fmt.Printf("\n%s%sAgent Commands:%s\n", ColorBold, ColorBlue, ColorReset)
					fmt.Printf("  %s/agent list%s       Show all available agents\n", ColorGreen, ColorReset)
					fmt.Printf("  %s/agent used%s       Show agents used in this chat\n", ColorGreen, ColorReset)
					fmt.Printf("  %s/agent add ...%s     Add agent: /agent add <name> <id> <path>\n", ColorGreen, ColorReset)
					fmt.Printf("  %s/agent activate%s   Activate agent: /agent activate <name> <prompt>\n", ColorGreen, ColorReset)
					fmt.Printf("  %s/agent deactivate%s Deactivate current agent\n", ColorGreen, ColorReset)
					continue
				}

				switch args[1] {
				case "list":
					agents := funcs.GetAgentTags()
					printBoxHeader("Available Agents", ColorBlue)
					if len(agents) == 0 {
						fmt.Printf("  %sNo agents configured%s\n", ColorYellow, ColorReset)
					} else {
						for _, v := range agents {
							modelName := funcs.GetModelName(uint32(v.Agent.AgentModel), "unknown")
							fmt.Printf("\n  %s[%s]%s %s\n", ColorGreen, v.ID, ColorReset, v.Agent.AgentName)
							fmt.Printf("      Model: %s\n", modelName)
							fmt.Printf("      Desc:  %s\n", v.Agent.AgentDescription)
						}
					}

				case "used":
					agents := unwrap(funcs.GetAgents(&session))
					fmt.Printf("\n%s%s┌─ Used Agents ─┐%s\n", ColorBold, ColorBlue, ColorReset)
					if len(agents) == 0 {
						fmt.Printf("  %sNo agents used in this chat%s\n", ColorYellow, ColorReset)
					} else {
						for _, v := range agents {
							fmt.Printf("  %s-%s Name: %s, ID: %s, Path: %s\n", ColorGreen, ColorReset, v.ID, v.AgentID, v.BindPath)
						}
					}

				case "add":
					if len(args) < 5 {
						fmt.Printf("%s❌ Usage: /agent add <name> <id> <path>%s\n", ColorRed, ColorReset)
						continue
					}
					err := funcs.AddAgent(&session, args[2], args[3], args[4])
					if err != nil {
						fmt.Printf("%s❌ Error adding agent: %v%s\n", ColorRed, err, ColorReset)
					}

				case "activate":
					if len(args) < 4 {
						fmt.Printf("%s❌ Usage: /agent activate <name> <prompt>%s\n", ColorRed, ColorReset)
						continue
					}
					err := funcs.ActivateAgent(&session, args[2], args[3])
					if err != nil {
						fmt.Printf("%s❌ Error activating agent: %v%s\n", ColorRed, err, ColorReset)
					}

				case "deactivate":
					funcs.DeactivateAgent(&session, "")
					fmt.Printf("%s✓ Agent deactivated%s\n", ColorGreen, ColorReset)

				default:
					fmt.Printf("%s❌ Unknown agent command: %s%s\n", ColorRed, args[1], ColorReset)
				}

			case "/scope":
				if len(args) < 2 {
					fmt.Printf("\n%s%sScope Commands:%s\n", ColorBold, ColorBlue, ColorReset)
					fmt.Printf("  %s/scope list%s        Show all available scopes\n", ColorGreen, ColorReset)
					fmt.Printf("  %s/scope enable <scope>%s  Enable a scope\n", ColorGreen, ColorReset)
					fmt.Printf("  %s/scope disable <scope>%s Disable a scope\n", ColorGreen, ColorReset)
					continue
				}

				switch args[1] {
				case "list":
					fmt.Printf("\n%s%s┌─ Available Scopes ─┐%s\n", ColorBold, ColorBlue, ColorReset)
					for _, v := range funcs.GetScopes() {
						enabled := " "
						if v.ID == "" {
							enabled = "✓"
							v.ID = "(default)"
						} else if session.EnableScopes[v.ID] {
							enabled = "✓"
						}
						fmt.Printf("  %s[%s]%s %-15s %s\n", ColorGreen, enabled, ColorReset, v.ID, v.Prompt)
					}

				case "enable":
					if len(args) < 3 {
						fmt.Printf("%s❌ Usage: /scope enable <scope>%s\n", ColorRed, ColorReset)
						continue
					}
					err := funcs.EnableScope(&session, args[2])
					if err != nil {
						fmt.Printf("%s❌ Error enabling scope: %v%s\n", ColorRed, err, ColorReset)
					}

				case "disable":
					if len(args) < 3 {
						fmt.Printf("%s❌ Usage: /scope disable <scope>%s\n", ColorRed, ColorReset)
						continue
					}
					err := funcs.DisableScope(&session, args[2])
					if err != nil {
						fmt.Printf("%s❌ Error disabling scope: %v%s\n", ColorRed, err, ColorReset)
					}

				default:
					fmt.Printf("%s❌ Unknown scope command: %s%s\n", ColorRed, args[1], ColorReset)
				}

			default:
				fmt.Printf("%s❌ Unknown command: %s%s\n", ColorRed, args[0], ColorReset)
				fmt.Printf("%sType /help for available commands%s\n", ColorBlue, ColorReset)
			}
			continue
		}
		err := funcs.UserAddMsg(&session, input, nil)
		if err != nil {
			fmt.Printf("%s❌ Error adding user message: %v%s\n", ColorRed, err, ColorReset)
		}

		// 显示 AI 响应
		runResponseLoop()
	}
}
