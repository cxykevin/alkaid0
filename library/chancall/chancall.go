// Package chancall 提供一个基于通道的异步调用机制，用于避免循环引用
package chancall

import "fmt"

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

const bufferSize = 64

var actChan = make(chan EventChan, bufferSize)

var consumers = make(map[string]func(any) (any, error))

// CallFunc 调用函数
type CallFunc func(obj any) (any, error)

// Register 注册消费者
func Register(consumer string, fn func(any) (any, error)) CallFunc {
	consumers[consumer] = fn
	fnc := (func(obj any) (any, error) {
		ev := EventChan{
			Consumer: consumer,
			In:       nil,
			Out:      make(chan Ret, 1),
		}
		ev.In = obj
		actChan <- ev
		ret := <-ev.Out
		return ret.Ret, ret.Err
	})
	return fnc
}

func start() {
	for ev := range actChan {
		consumer, ok := consumers[ev.Consumer]
		if !ok {
			ev.Out <- Ret{Ret: nil, Err: fmt.Errorf("consumer %s not found", ev.Consumer)}
			continue
		}
		ret, err := consumer(ev.In)
		ev.Out <- Ret{
			Ret: ret,
			Err: err,
		}
		close(ev.Out)
	}
}

func init() {
	go start()
}
