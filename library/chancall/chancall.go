// Package chancall 提供一个基于通道的异步调用机制，用于避免循环引用
package chancall

import (
	"fmt"
	"log"
	"sync"
)

// Ret 调用结果
type Ret struct {
	Ret any
	Err error
}

// EventChan 事件通道
type EventChan struct {
	Consumer string
	In       any
	Out      chan Ret
}

const bufferSize = 4096

// ActChan 事件通道
var ActChan = make(chan EventChan, bufferSize)

var (
	consumers   = make(map[string]func(any) (any, error))
	consumersMu sync.RWMutex
)

// CallFunc 调用函数
type CallFunc func(obj any) (any, error)

// Register 注册消费者
func Register(consumer string, fn func(any) (any, error)) CallFunc {
	consumersMu.Lock()
	consumers[consumer] = fn
	consumersMu.Unlock()
	fnc := (func(obj any) (any, error) {
		ev := EventChan{
			Consumer: consumer,
			In:       nil,
			Out:      make(chan Ret, 1),
		}
		ev.In = obj
		ActChan <- ev
		ret := <-ev.Out
		return ret.Ret, ret.Err
	})
	return fnc
}

func start() {
	for ev := range ActChan {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("chancall: panic in consumer %s: %v", ev.Consumer, r)
					// 必须向调用方回复并关闭通道，否则调用方在 `<-ev.Out` 永久阻塞（死锁）
					ev.Out <- Ret{Ret: nil, Err: fmt.Errorf("consumer %s panicked: %v", ev.Consumer, r)}
					close(ev.Out)
				}
			}()
			consumersMu.RLock()
			consumer, ok := consumers[ev.Consumer]
			consumersMu.RUnlock()
			if !ok {
				ev.Out <- Ret{Ret: nil, Err: fmt.Errorf("consumer %s not found", ev.Consumer)}
				return
			}
			ret, err := consumer(ev.In)
			ev.Out <- Ret{
				Ret: ret,
				Err: err,
			}
			close(ev.Out)
		}()
	}
}

func init() {
	go start()
}
