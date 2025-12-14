package stack

// Stack 结构体表示一个栈
type Stack struct {
	items []any // 使用any切片来存储任意类型的元素
}

// New 创建并返回一个新的栈
func New() *Stack {
	return &Stack{
		items: make([]any, 0),
	}
}

// Push 将元素压入栈顶
func (s *Stack) Push(item any) {
	s.items = append(s.items, item)
}

// Pop 弹出栈顶元素
func (s *Stack) Pop() (any, bool) {
	if len(s.items) == 0 {
		return nil, false
	}
	item := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return item, true
}

// Top 查看栈顶元素但不移除
func (s *Stack) Top() (any, bool) {
	if len(s.items) == 0 {
		return nil, false
	}
	return s.items[len(s.items)-1], true
}

// IsEmpty 检查栈是否为空
func (s *Stack) IsEmpty() bool {
	return len(s.items) == 0
}

// Size 返回栈中元素的数量
func (s *Stack) Size() int {
	return len(s.items)
}

// Bottom 查看栈底元素（最先入栈的元素）
func (s *Stack) Bottom() (any, bool) {
	if len(s.items) == 0 {
		return nil, false
	}
	return s.items[0], true
}
