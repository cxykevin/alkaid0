//go:build ignore

package main

import "github.com/cxykevin/alkaid0/mock/openai"

func main() {
	openai.StartServerTask()
	select {}
}
