// Package config 定义 Relay 与 Agent 的配置结构、加载与校验。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadFile 读取并解析 YAML 配置到 out，执行环境变量展开与 Validate。
func LoadFile(path string, out interface{ Validate() error }) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := applyEnvExpansion(out); err != nil {
		return fmt.Errorf("config: env expansion %s: %w", path, err)
	}
	if err := out.Validate(); err != nil {
		return fmt.Errorf("config: validate %s: %w", path, err)
	}
	return nil
}

// applyEnvExpansion 遍历配置中的字符串字段，展开 ${VAR} / ${VAR:default}。
// 通过 JSON 往返实现通用遍历（配置结构无自定义 JSON 编解码）。
func applyEnvExpansion(out any) error {
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	var tree any
	if err := json.Unmarshal(b, &tree); err != nil {
		return err
	}
	tree = expandTree(tree)
	b2, err := json.Marshal(tree)
	if err != nil {
		return err
	}
	return json.Unmarshal(b2, out)
}

func expandTree(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = expandTree(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = expandTree(val)
		}
		return t
	case string:
		return expandEnv(t)
	default:
		return v
	}
}

// 环境变量引用支持：值为 `${VAR}` 或 `${VAR:default}` 时从环境读取。
func expandEnv(s string) string {
	if !strings.HasPrefix(s, "${") || !strings.HasSuffix(s, "}") {
		return s
	}
	inner := s[2 : len(s)-1]
	name, def, hasDef := strings.Cut(inner, ":")
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	if hasDef {
		return def
	}
	return s
}
