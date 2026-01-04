package loop

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/provider/request"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/index"
)

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
	reader := bufio.NewReader(os.Stdin)
	chats := []structs.Chats{}
	assert(storage.DB.Find(&chats).Error)
	fmt.Println("Init tools..")
	index.Load()
	fmt.Println("\033[2J")
	fmt.Println("===== Chats =====")
	for idx, v := range chats {
		fmt.Printf("- [%d] ID: %v\n", idx+1, v.ID)
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
		// 去掉\n
		input = input[:len(input)-1]
		inputNum, err := strconv.Atoi(input)
		if err != nil {
			fmt.Println("input error")
			continue
		}
		if inputNum < 0 {
			// 删除
			assert(storage.DB.Delete(&structs.Chats{}, -inputNum).Error)
			assert(storage.DB.Find(&chats).Error)
			fmt.Println("===== Chats =====")
			for idx, v := range chats {
				fmt.Printf("- [%d] ID: %v\n", idx+1, v.ID)
			}
		} else {
			// 创建
			if inputNum == 0 {
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
	// 显示历史
	fmt.Println("===== History =====")
	chatMsgs := []structs.Messages{}
	assert(storage.DB.Where("chat_id = ?", chats[chatNum].ID).Find(&chatMsgs).Error)
	for _, v := range chatMsgs {
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
	// 获取用户输入
	for {
		var input string
		fmt.Print("> ")
		input = unwrap(reader.ReadString('\n'))
		input = input[:len(input)-1]
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
