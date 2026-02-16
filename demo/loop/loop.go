package loop

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/request"
	"github.com/cxykevin/alkaid0/provider/request/agents"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/toolobj"
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

// getModelName 获取模型名称
func getModelName(modelID uint32) string {
	if modelID == 0 {
		return "unknown"
	}
	if modelInfo, ok := config.GlobalConfig.Model.Models[int32(modelID)]; ok {
		return modelInfo.ModelName
	}
	return "unknown"
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
	chats := []structs.Chats{}
	assert(db.Find(&chats).Error)

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
		fmt.Printf("  %s[ 1-%d]%s %sEnter existing chat%s\n", ColorGreen, len(chats), ColorReset, ColorBlue, ColorReset)
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

			assert(db.Delete(&structs.Chats{}, deletedChat.ID).Error)
			assert(db.Find(&chats).Error)

			fmt.Printf("%s✓ Chat #%d deleted successfully%s\n", ColorGreen, deletedChat.ID, ColorReset)
			showChatList()
		} else {
			// 创建或进入聊天
			if inputNum == 0 {
				logger.Info("create new chat")
				newChat := &structs.Chats{}
				assert(db.Create(newChat).Error)
				assert(db.Find(&chats).Error)
				inputNum = len(chats)
				fmt.Printf("%s✓ New chat created (ID: %d)%s\n", ColorGreen, newChat.ID, ColorReset)
			}

			if inputNum < 1 || inputNum > len(chats) {
				fmt.Printf("%s❌ Invalid chat number: %d (valid range: 1-%d)%s\n", ColorRed, inputNum, len(chats), ColorReset)
				continue
			}

			chatNum = inputNum - 1
			flag = false
		}
	}
	session := chats[chatNum]
	session.DB = db
	session.TemporyDataOfSession = make(map[string]any)
	actions.Load(&session)

	// 显示会话信息
	fmt.Printf("\n%s%s╔════════════════════════════════════════════════════════════╗%s\n", ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s║%s  Chat Session: %s%-44d%s%s%s║%s\n", ColorBold, ColorCyan, ColorReset, ColorYellow, session.ID, ColorReset, ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s╚════════════════════════════════════════════════════════════╝%s\n", ColorBold, ColorCyan, ColorReset)

	// 显示配置信息
	printBoxHeader("Configuration", ColorPurple)
	modelName := getModelName(session.LastModelID)
	fmt.Printf("  %sModel:%s  %s\n", ColorBlue, ColorReset, modelName)
	if session.NowAgent != "" {
		fmt.Printf("  %sAgent:%s  %s\n", ColorBlue, ColorReset, session.NowAgent)
	} else {
		fmt.Printf("  %sAgent:%s  %s(none)%s\n", ColorBlue, ColorReset, ColorYellow, ColorReset)
	}

	storage.GlobalConfig.LastChatID = session.ID
	logger.Debug("use chat ID:%v|Agent:%v|Model:%v", session.ID, session.NowAgent, session.LastModelID)

	// 显示历史消息
	fmt.Printf("\n%s%s╔════════════════════════════════════════════════════════════╗%s\n", ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s║%s  Conversation History                                      %s%s║%s\n", ColorBold, ColorCyan, ColorReset, ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s╚════════════════════════════════════════════════════════════╝%s\n", ColorBold, ColorCyan, ColorReset)

	chatMsgs := []structs.Messages{}
	assert(db.Where("chat_id = ?", session.ID).Order("id ASC").Find(&chatMsgs).Error)

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
				modelDisplay := getModelName(v.ModelID)

				fmt.Printf("\n%s%s┌─ AI (%s%s%s", ColorBold, ColorBlue, ColorReset, modelDisplay, ColorBold+ColorBlue)
				if v.AgentID != nil && *v.AgentID != "" {
					fmt.Printf(" | Agent: %s%s%s", ColorReset, *v.AgentID, ColorBold+ColorBlue)
				}
				fmt.Printf("%s) ─┐%s\n", ColorBold+ColorBlue, ColorReset)

				if v.ThinkingDelta != "" {
					fmt.Printf("%s[Thinking]%s %s\n", ColorPurple, ColorReset, v.ThinkingDelta)
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

	// 获取用户输入
	for {
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
				fmt.Printf("\n%sShortcuts:%s\n", ColorBold, ColorReset)
				fmt.Printf("  %s!%s Repeat last input\n", ColorGreen, ColorReset)

			case "/exit":
				fmt.Printf("\n%s%sGoodbye!%s\n", ColorBold, ColorCyan, ColorReset)
				os.Exit(0)

			case "/models":
				printBoxHeader("Available Models", ColorBlue)
				if len(config.GlobalConfig.Model.Models) == 0 {
					fmt.Printf("  %sNo models configured%s\n", ColorYellow, ColorReset)
				} else {
					for k, v := range config.GlobalConfig.Model.Models {
						marker := " "
						if uint32(k) == session.LastModelID {
							marker = "✓"
						}
						fmt.Printf("  %s[%s]%s [%2d] %s%-20s%s (%s)\n",
							ColorGreen, marker, ColorReset, k, ColorBold, v.ModelName, ColorReset, v.ModelID)
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
				modelInfo, ok := config.GlobalConfig.Model.Models[int32(modelID)]
				if !ok {
					fmt.Printf("%s❌ Model not found: %d%s\n", ColorRed, modelID, ColorReset)
					continue
				}
				session.LastModelID = uint32(modelID)
				assert(db.Save(&session).Error)
				fmt.Printf("%s✓ Model changed to: %s%s%s\n", ColorGreen, ColorBold, modelInfo.ModelName, ColorReset)

			case "/summary":
				fmt.Printf("\n%s%sGenerating summary...%s\n", ColorBold, ColorYellow, ColorReset)
				ret, err := request.SummarySession(context.Background(), &session)
				if err != nil {
					fmt.Printf("%s❌ Error generating summary:%s\n%v\n", ColorRed, ColorReset, err)
				} else {
					fmt.Printf("\n%s%s╔════════════════════════════════════════════════════════════╗%s\n", ColorBold, ColorCyan, ColorReset)
					fmt.Printf("%s%s║%s  Conversation Summary                                      %s%s║%s\n", ColorBold, ColorCyan, ColorReset, ColorBold, ColorCyan, ColorReset)
					fmt.Printf("%s%s╚════════════════════════════════════════════════════════════╝%s\n", ColorBold, ColorCyan, ColorReset)
					fmt.Printf("\n%s\n", ret)
				}

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
					printBoxHeader("Available Agents", ColorBlue)
					if len(config.GlobalConfig.Agent.Agents) == 0 {
						fmt.Printf("  %sNo agents configured%s\n", ColorYellow, ColorReset)
					} else {
						for k, v := range config.GlobalConfig.Agent.Agents {
							modelName := getModelName(uint32(v.AgentModel))
							fmt.Printf("\n  %s[%s]%s %s\n", ColorGreen, k, ColorReset, v.AgentName)
							fmt.Printf("      Model: %s\n", modelName)
							fmt.Printf("      Desc:  %s\n", v.AgentDescription)
						}
					}

				case "used":
					agents := unwrap(agents.ListAgents(db))
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
					agents.AddAgent(&session, args[2], args[3], args[4])

				case "activate":
					if len(args) < 4 {
						fmt.Printf("%s❌ Usage: /agent activate <name> <prompt>%s\n", ColorRed, ColorReset)
						continue
					}
					agents.ActivateAgent(&session, args[2], args[3])

				case "deactivate":
					agents.DeactivateAgent(&session, "")
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
					for k, v := range toolobj.Scopes {
						enabled := " "
						if k == "" {
							enabled = "✓"
							k = "(default)"
						} else if session.EnableScopes[k] {
							enabled = "✓"
						}
						fmt.Printf("  %s[%s]%s %-15s %s\n", ColorGreen, enabled, ColorReset, k, v)
					}

				case "enable":
					if len(args) < 3 {
						fmt.Printf("%s❌ Usage: /scope enable <scope>%s\n", ColorRed, ColorReset)
						continue
					}
					actions.EnableScope(&session, args[2])

				case "disable":
					if len(args) < 3 {
						fmt.Printf("%s❌ Usage: /scope disable <scope>%s\n", ColorRed, ColorReset)
						continue
					}
					actions.DisableScope(&session, args[2])

				default:
					fmt.Printf("%s❌ Unknown scope command: %s%s\n", ColorRed, args[1], ColorReset)
				}

			default:
				fmt.Printf("%s❌ Unknown command: %s%s\n", ColorRed, args[0], ColorReset)
				fmt.Printf("%sType /help for available commands%s\n", ColorBlue, ColorReset)
			}
			continue
		}
		request.UserAddMsg(&session, input, nil)

		// 显示 AI 响应
		fmt.Printf("\n%s%s┌─ AI Response ─┐%s\n", ColorBold, ColorBlue, ColorReset)

		// 启动 loop
		for {
			thinkingFlag := false
			responseStarted := false

			finish, err := request.SendRequest(context.Background(), &session, func(delta string, thinkingDelta string) error {
				if thinkingDelta != "" {
					if !thinkingFlag {
						thinkingFlag = true
					}
					fmt.Printf("%s%s%s ", ColorPurple, thinkingDelta, ColorReset)
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

			if thinkingFlag {
				fmt.Printf("\n")
			}

			if err != nil {
				fmt.Printf("\n%s❌ Error:%s\n%v\n", ColorRed, ColorReset, err)
				break
			}

			if finish {
				if !responseStarted && !thinkingFlag {
					fmt.Printf("%s(No response)%s\n", ColorYellow, ColorReset)
				}
				break
			}
		}
	}
}
