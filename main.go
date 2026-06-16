package v2net_core

import (
	"github.com/xtls/xray-core/core"
)

// HelloCore экспортируется с большой буквы, чтобы Kotlin его увидел.
// Функция возвращает строчку с реальной версией ядра Xray.
func HelloCore() string {
	return "Xray core is alive: " + core.Version()
}