package store

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// configVersion 是 config.json 的结构版本；读取时以当前版本为准，结构
// 变更在这里做迁移。
const configVersion = 1

// Config 是应用级配置（config.json），跨 tab 共享、重启后保留。
// 目前只有 diff 查看偏好。
type Config struct {
	Version int       `json:"version"`
	Diff    DiffPrefs `json:"diff"`
}

// DiffPrefs 是 diff 查看的全局偏好（三个开关各只有一个值，不按文件/tab
// 区分）：显式切换即落盘，打开内容只读不写。
type DiffPrefs struct {
	Layout   int  `json:"layout"`   // 0=统一 1=分栏
	Expand   bool `json:"expand"`   // 展开未修改
	ShowDiff bool `json:"showDiff"` // 目录列表「查看 diff」
}

// LoadConfig 读取应用配置；文件缺失或损坏返回默认值（统一布局、折叠
// 未修改、不勾选「查看 diff」）。非法 layout 收敛到统一，避免坏值让
// 官方 diff 视图拿到未知布局。
func LoadConfig(dir string) Config {
	cfg := Config{Version: configVersion}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return cfg
	}
	var f Config
	if err := json.Unmarshal(data, &f); err != nil {
		return cfg
	}
	f.Version = configVersion
	if f.Diff.Layout != 1 {
		f.Diff.Layout = 0
	}
	return f
}

// SaveConfig 持久化应用配置。偏好是便利功能：写失败由调用方静默，
// 内存值仍然生效（下次显式操作会重试）。
func SaveConfig(dir string, c Config) error {
	c.Version = configVersion
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644)
}
