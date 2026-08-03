package startup

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	_ "embed" // embed logo

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/context/codebase"
	"github.com/cxykevin/alkaid0/context/lsp"
	"github.com/cxykevin/alkaid0/helper"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/product"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/request"
	"github.com/cxykevin/alkaid0/provider/request/build"
	reqstructs "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/server"
	"github.com/cxykevin/alkaid0/server/client/jsonrpc/connect"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/tools/fetch"
	"github.com/cxykevin/alkaid0/tools/tools/search"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
)

const alkaid0IgnoreEntry = "\n# alkaid0\n.alkaid0/\n.alk_*\n"

var logger = log.New("startup")

//go:embed logo.ansi
var logoString string

var versionTemplate = fmt.Sprintf(`
Version:
    Version:      %s (Number %d)
    Commit ID:    %s
Build:
    Time:         %d
    Note:         %s
System:
    OS:           %s
    Arch:         %s
    Current Time: %d
Network:
    User Agent:   %s

%s// if(alkaid0.works){ do_not_panic(); }%s
`,
	product.Version,
	product.VersionID,
	product.CommitID,
	product.BuildTime,
	product.BuildNote,
	runtime.GOOS,
	runtime.GOARCH,
	time.Now().Unix(),
	product.UserAgent,
	"\033[2m\033[3m",
	"\033[0m",
)

var helpTemplate = fmt.Sprintf(`
Usage:
    alkaid0 [command]
Commands:
    help      Show this help message and exit
    version   Show version information and exit
    acp       Start the alkaid0 helper
              (Use alkaid0 acp --help for more information)
    [empty]   Start the server
Environment Variables:
    ALKAID0_DEBUG        Enable debug mode (true | false)
    ALKAID0_LOG_LEVEL    Set log level (default: info)
                         (debug | info | warn | error)
    ALKAID0_LOG_PATH     Set log file path
    ALKAID0_CONFIG_PATH  Set config file path

%s// if(alkaid0.works){ do_not_panic(); }%s
`,
	"\033[2m\033[3m",
	"\033[0m",
)

// Startup 启动程序
func Startup() {
	if len(os.Args) >= 2 && os.Args[1] == "acp" {
		helper.StartHelper(os.Args[1:])
		// helper 会话结束后直接返回，避免继续执行并启动完整的 WebSocket 服务器
		return
	}

	fmt.Fprintln(os.Stderr, logoString)

	if len(os.Args) >= 2 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println(versionTemplate)
		os.Exit(0)
		return
	}

	if len(os.Args) >= 2 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h" || os.Args[1] == "/?" || os.Args[1] == "/help" || os.Args[1] == "/H" || os.Args[1] == "/HELP") {
		fmt.Println(helpTemplate)
		os.Exit(0)
		return
	}

	logger.Info("starting alkaid0...")

	// 设置 Go 运行时内存软限制，让 GC 在内存超限时更积极回收并归还给 OS
	// 避免 idle 时 Go 运行时持有过多不释放的内存
	debug.SetMemoryLimit(256 * 1024 * 1024) // 256MB

	openai.Start()
	config.Load()
	log.Load()
	if os.Getenv("ALKAID0_DEBUG") != "true" {
		defer log.SolvePanic()
	}
	ensureGlobalGitIgnore()
	index.Load()

	// Codebase 搜索引擎初始化
	if err := codebase.Initialize(); err != nil {
		logger.Warn("codebase init: %v (context search unavailable)", err)
	}
	search.SetContextSearchFn(func(ctx context.Context, directory string, searchType int, query string, limit int) ([]search.ContextSearchResult, error) {
		// 检查 codebase 是否有索引数据，没有则跳过（避免空调用 embedding API）
		paths, err := codebase.GetFilePaths(directory)
		if err != nil || len(paths) == 0 {
			if err != nil {
				logger.Debug("codebase get paths: %v (skipping context search)", err)
			}
			return nil, nil
		}
		results, err := codebase.Search(ctx, directory, codebase.SearchType(searchType), query, limit)
		if err != nil {
			return nil, err
		}
		out := make([]search.ContextSearchResult, len(results))
		for i, r := range results {
			out[i] = search.ContextSearchResult{
				FilePath: r.FilePath,
				Symbol:   r.Symbol,
				Content:  r.EmbedText,
				Score:    r.Score,
				Tags:     r.Tags,
			}
		}
		return out, nil
	})

	// 在线搜索结果总结注入
	search.SetSummarizeFn(func(ctx context.Context, rawResult, query string, modelID int32) (string, error) {
		modelConfig, err := build.GetModelConfig(modelID)
		if err != nil {
			return "", fmt.Errorf("get model config: %w", err)
		}

		// 渲染总结提示词模板（注入原始搜索问题）
		summaryTmpl := prompts.Load("search_summary", search.SummaryPrompt)
		systemPrompt, err := prompts.Render(summaryTmpl, map[string]string{"Query": query})
		if err != nil {
			logger.Warn("render search summary prompt: %v, fallback to raw", err)
			systemPrompt = search.SummaryPrompt
		}

		// 拼接搜索问题 + 原始结果，让 AI 根据问题总结相关内容
		userContent := fmt.Sprintf("Search query: %s\n\nSearch results:\n%s", query, rawResult)

		messages := []reqstructs.Message{
			{Role: reqstructs.RoleSystem, Content: systemPrompt},
			{Role: reqstructs.RoleUser, Content: userContent},
		}

		req := reqstructs.ChatCompletionRequest{
			Messages: messages,
		}

		var sb strings.Builder
		err = request.SimpleOpenAIRequest(ctx, modelConfig.ProviderURL, modelConfig.ProviderKey,
			modelConfig.ModelID, req, nil,
			func(resp reqstructs.ChatCompletionResponse) error {
				if len(resp.Choices) > 0 {
					sb.WriteString(resp.Choices[0].Delta.Content)
				}
				return nil
			})
		if err != nil {
			return "", fmt.Errorf("summarize request: %w", err)
		}

		result := strings.TrimSpace(sb.String())
		if result == "" {
			return "", fmt.Errorf("summarize returned empty result")
		}
		return result, nil
	})

	// fetch 工具抓取内容总结注入（模型直接共用 SearchSummaryModel）
	fetch.SetSummarizeFn(func(ctx context.Context, rawContent, summaryPrompt string, modelID int32) (string, error) {
		modelConfig, err := build.GetModelConfig(modelID)
		if err != nil {
			return "", fmt.Errorf("get model config: %w", err)
		}

		systemPrompt := "你是网页内容总结助手。把抓取到的内容总结成 markdown：保留关键事实、代码块与链接，输出语言与总结提示词一致。"

		userContent := fmt.Sprintf("Summary instructions: %s\n\nRaw content:\n%s", summaryPrompt, rawContent)

		messages := []reqstructs.Message{
			{Role: reqstructs.RoleSystem, Content: systemPrompt},
			{Role: reqstructs.RoleUser, Content: userContent},
		}

		req := reqstructs.ChatCompletionRequest{
			Messages: messages,
		}

		var sb strings.Builder
		err = request.SimpleOpenAIRequest(ctx, modelConfig.ProviderURL, modelConfig.ProviderKey,
			modelConfig.ModelID, req, nil,
			func(resp reqstructs.ChatCompletionResponse) error {
				if len(resp.Choices) > 0 {
					sb.WriteString(resp.Choices[0].Delta.Content)
				}
				return nil
			})
		if err != nil {
			return "", fmt.Errorf("summarize request: %w", err)
		}

		result := strings.TrimSpace(sb.String())
		if result == "" {
			return "", fmt.Errorf("summarize returned empty result")
		}
		return result, nil
	})

	// Trace 后台索引注入（打 tempfs 标签）
	trace.SetIndexTaskFn(func(directory string, filePath string, fullContent string, embedText string, tags []string) error {
		if same, _ := codebase.CheckContentHash(directory, filePath, "", embedText); !same {
			return codebase.AddToQueue(directory, codebase.EmbedTask{
				FilePath:    filePath,
				FullContent: fullContent,
				EmbedText:   embedText,
				Tags:        tags,
			})
		}
		return nil
	})

	// LSP 客户端初始化
	if err := lsp.Initialize(); err != nil {
		logger.Warn("LSP init: %v (continuing without LSP)", err)
	}

	// 设置信号处理：SIGTERM/SIGINT/SIGQUIT 触发优雅关闭
	// 30 秒超时后强制退出
	// 当 config.IgnoreSignals 为 true 时跳过信号处理注册，忽略所有信号
	if !config.GlobalConfig.IgnoreSignals {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
		defer stop()

		go func() {
			<-ctx.Done()
			logger.Info("received shutdown signal, initiating graceful shutdown...")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := connect.ShutdownWs(shutdownCtx); err != nil {
				logger.Warn("ws server shutdown: %v", err)
			}
			if err := lsp.Shutdown(); err != nil {
				logger.Warn("LSP shutdown: %v", err)
			}
			log.Shutdown()
			os.Exit(0)
		}()
	} else {
		logger.Info("signal handling disabled by config (ignoreSignals=true)")
	}

	// 读取环境变量 ALKAID0_WORKDIR
	if workdir := os.Getenv("ALKAID0_WORKDIR"); workdir != "" {
		logger.Info("changing workdir to: %s", workdir)
		// 设置工作目录
		_ = os.Chdir(workdir)
	}

	logger.Info("Start server...")
	server.Start()
}
