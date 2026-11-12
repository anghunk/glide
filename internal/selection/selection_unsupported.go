//go:build !darwin && !windows

package selection

import "context"

// Status 返回当前系统的能力；未实现的平台明确提供剪贴板替代路径。
func Status() Capability { return Capability{Message: ErrUnsupported.Error()} }

func readNative() (Selection, error) { return Selection{}, ErrUnsupported }

// StartWatching 在不支持的系统返回明确错误。
func StartWatching(context.Context, func(Selection)) (func(), error) { return nil, ErrUnsupported }

// OpenPermissionSettings 在不支持的平台返回能力错误。
func OpenPermissionSettings() error { return ErrUnsupported }
