package elements

// Color 颜色主题
type Color uint8

// 颜色枚举
const (
	ColorBg Color = iota
	ColorFg
	ColorSecBg
	ColorSecFg
	ColorThirdBg
	ColorThirdFg
	ColorError
	ColorWarning
	ColorInfo
	ColorSuccess
)

// BoxBase 容器盒
type BoxBase struct {
	Children   []*BoxBase
	ID         string
	BgColor    Color
	FgColor    Color
	HoverColor Color
}

// HBox 水平容器
type HBox struct {
	BoxBase
}

// VBox 竖直容器
type VBox struct {
	BoxBase
}

// HSplitBox 水平分割容器
type HSplitBox struct {
	LeftChild  *BoxBase
	RightChild *BoxBase
	LeftRatio  float32
	BoxBase
}

// VSplitBox 竖直分割容器
type VSplitBox struct {
	TopChild    *BoxBase
	BottomChild *BoxBase
	TopRatio    float32
	BoxBase
}

// Text 文本
type Text struct {
	Text string
	BoxBase
}

// Button 按钮
type Button struct {
	Text    string
	EventID string
	BoxBase
}

// CheckBoxGroupValue 复选框值
type CheckBoxGroupValue uint32

// CheckBox 复选框
type CheckBox struct {
	Text    string
	EventID string
	Group   *RadioBoxGroupValue
	Value   uint32
	BoxBase
}

// RadioBoxGroupValue 单选框值
type RadioBoxGroupValue uint32

// RadioBox 单选框
type RadioBox struct {
	Text    string
	EventID string
	Group   *RadioBoxGroupValue
	Value   uint32
	BoxBase
}

// BorderBox 边框容器
type BorderBox struct {
	BoxBase
}

// Input 输入框
type Input struct {
	Group       *string
	Placeholder string
	BoxBase
}

// IBox 接口
type IBox interface {
	BoxBase
}
