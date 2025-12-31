package structs

// Scopes 存储命名空间启用状态
type Scopes struct {
    Name    string `gorm:"primaryKey"`
    Enabled bool
}
