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
	"github.com/cxykevin/alkaid0/tools/index"
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
func stringDefault(str *string) string {
	if str != nil {
		return *str
	}
	return ""
}

// Start 启动 Demo Loop
func Start() {
	logger.Info("loop initing")
	reader := bufio.NewReader(os.Stdin)
	chats := []structs.Chats{}
	assert(storage.DB.Find(&chats).Error)
	fmt.Println("Init tools..")
	index.Load()
	fmt.Println("\033[2J")
	fmt.Println("===== Chats =====")
	for idx, v := range chats {
		fmt.Printf("- [%d] ID: %v\n", idx+1, v.ID)
		logger.Debug("(chats)discover chat %d,%v", idx+1, v.ID)
	}
	fmt.Println("] num : into")
	fmt.Println("] 0   : create")
	fmt.Println("] -num: delete")
	var input string
	chatNum := 0
	flag := true
	for flag {
		fmt.Printf("DO> ")
		input = unwrap(reader.ReadString('\n'))
		// 去掉换行符（兼容Windows的\r\n和Linux的\n）
		input = strings.TrimSpace(input)
		logger.Debug("user input: %v", input)
		inputNum, err := strconv.Atoi(input)
		if err != nil {
			fmt.Println("input error")
			continue
		}
		if inputNum < 0 {
			// 删除
			if inputNum > len(chats) {
				fmt.Println("input error")
				continue
			}
			logger.Info("delete chat %d", inputNum)
			assert(storage.DB.Delete(&structs.Chats{}, chats[-inputNum].ID).Error)
			assert(storage.DB.Find(&chats).Error)
			fmt.Println("===== Chats =====")
			for idx, v := range chats {
				fmt.Printf("- [%d] ID: %v\n", idx+1, v.ID)
				logger.Debug("(chats)discover chat %d,%v", idx+1, v.ID)
			}
		} else {
			// 创建
			if inputNum == 0 {
				logger.Info("create chat")
				assert(storage.DB.Create(&structs.Chats{}).Error)
				assert(storage.DB.Find(&chats).Error)
				inputNum = len(chats)
			}
			if inputNum > len(chats) {
				fmt.Println("input error")
				continue
			} else {
				chatNum = inputNum - 1
				flag = false
			}
		}
	}
	fmt.Println("===== Info =====")
	fmt.Printf("ID: %v\n", chats[chatNum].ID)
	fmt.Printf("Agent: %v\n", chats[chatNum].NowAgent)
	fmt.Printf("Model: %v\n", chats[chatNum].LastModelID)
	storage.GlobalConfig.CurrentChatID = chats[chatNum].ID
	logger.Debug("use chat ID:%v|Agent:%v|Model:%v", chats[chatNum].ID, chats[chatNum].NowAgent, chats[chatNum].LastModelID)
	// 显示历史
	fmt.Println("===== History =====")
	chatMsgs := []structs.Messages{}
	assert(storage.DB.Where("chat_id = ?", chats[chatNum].ID).Find(&chatMsgs).Error)
	for _, v := range chatMsgs {
		logger.Debug("(history)discover history %v", strings.ReplaceAll(fmt.Sprintf("%v", v), "\n", "\\n"))
		fmt.Print("--- ")
		switch v.Type {
		case 0:
			fmt.Println("User")
		case 1:
			fmt.Println("AI")
			fmt.Printf("Model: %v  Agent: %v\n", v.ModelID, stringDefault(v.AgentID))
		case 2:
			fmt.Println("Tool")
		}
		if v.ThinkingDelta != "" {
			fmt.Printf("[Think]%v\n", v.ThinkingDelta)
		}
		fmt.Printf("%v\n", v.Delta)
	}
	fmt.Println("===== Input =====")
	fmt.Println("] /help: show command help")
	fmt.Println("] !    : continue last input")
	// 获取用户输入
	for {
		var input string
		fmt.Print("> ")
		input = unwrap(reader.ReadString('\n'))
		input = strings.TrimSpace(input)
		logger.Debug("user input: %v", input)
		if input == "" {
			continue
		}
		if input[0] == '/' {
			args := strings.Split(input, " ")
			switch args[0] {
			case "/help":
				fmt.Println("] /help: show command help")
				fmt.Println("] /exit: exit loop")
				fmt.Println("] /model [id]: set model")
				fmt.Println("] /models: get models list")
				fmt.Println("] /summary: summary the history")
				fmt.Println("] /agent: manage agents (only for test)")
			case "/exit":
				os.Exit(0)
			case "/models":
				for k, v := range config.GlobalConfig.Model.Models {
					fmt.Printf("- [ID:%d] %v(%v)\n", k, v.ModelName, v.ModelID)
				}
			case "/model":
				if len(args) < 2 {
					fmt.Println("input error(args not enough)")
					continue
				}
				modelID, err := strconv.Atoi(args[1])
				if err != nil {
					fmt.Println("input error(not int)")
					continue
				}
				modelInfo, ok := config.GlobalConfig.Model.Models[int32(modelID)]
				if !ok {
					fmt.Println("input error(model not found)")
					continue
				}
				chats[chatNum].LastModelID = uint32(modelID)
				// 写数据库
				assert(storage.DB.Save(&chats[chatNum]).Error)
				fmt.Printf("- model changed to %v(%v)\n", modelInfo.ModelName, modelInfo.ModelID)
			case "/summary":
				fmt.Printf("summarying...\n")
				ret, err := request.Summary(context.Background(), chats[chatNum].ID, "")
				fmt.Printf("summary finished!\n%s\n", ret)
				if err != nil {
					fmt.Printf("Err!\n%v\n", err)
				}
			case "/agent":
				if len(args) < 2 {
					// fmt.Println("] TODO:")
					fmt.Println("] /agent list: show agents")
					fmt.Println("] /agent used: show used agents")
					fmt.Println("] /agent add [name] [id] [path]: add agents to project")
					fmt.Println("] /agent activate [name] [prompt]: activate agent")
					fmt.Println("] /agent deactive: deactivate agent")
					continue
				}
				switch args[1] {
				case "list":
					for k, v := range config.GlobalConfig.Agent.Agents {
						modelName := "unknown"

						if modelInfo, ok := config.GlobalConfig.Model.Models[int32(v.AgentModel)]; ok {
							modelName = modelInfo.ModelName
						}
						fmt.Printf("- [ID:%s] %v(model: [%d](%v))\nDescription: %v\nPrompt: %v\n", k, v.AgentName, v.AgentModel, modelName, v.AgentDescription, v.AgentPrompt)
					}

				case "used":
					agents := unwrap(agents.ListAgents())
					for _, v := range agents {
						fmt.Printf("- [ID:%s] Model: %s, Path: %s\n", v.ID, v.AgentID, v.BindPath)
					}
				case "add":
					if len(args) < 5 {
						fmt.Println("input error(args not enough)")
						continue
					}
					agents.AddAgent(args[2], args[3], args[4])
				case "activate":
					if len(args) < 4 {
						fmt.Println("input error(args not enough)")
						continue
					}
					agents.ActivateAgent(chats[chatNum].ID, args[2], args[3])
				case "deactivate":
					agents.DeactivateAgent(chats[chatNum].ID)
				}
			}
			continue
		}
		if input != "!" {
			request.UserAddMsg(storage.DB, chats[chatNum].ID, input, nil)
		}
		// 启动 loop
		for {
			fmt.Println("--- AI")
			thinkingFlag := false
			finish, err := request.SendRequest(context.Background(), chats[chatNum].ID, func(delta string, thinkingDelta string) error {
				if thinkingDelta != "" {
					if !thinkingFlag {
						fmt.Print("[Think]")
					}
					thinkingFlag = true
					fmt.Print(thinkingDelta)
				}
				if delta != "" {
					if thinkingFlag {
						fmt.Print("\n")
					}
					thinkingFlag = false
					fmt.Print(delta)
				}
				return nil
			})
			fmt.Print("\n")
			if err != nil {
				fmt.Printf("Err!\n%v\n", err)
				break
			}
			if finish {
				break
			}
		}
	}
}
