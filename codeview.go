package main

import (
	"strings"
)

// imageExtensions 支持预览的图片扩展名（ui.DecodeBitmap 支持的格式）。
var imageExtensions = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "bmp": true,
}

// isImagePath 按扩展名判断是否可作图片预览。
func isImagePath(path string) bool {
	dot := strings.LastIndex(path, ".")
	if dot == -1 {
		return false
	}
	return imageExtensions[strings.ToLower(path[dot+1:])]
}
