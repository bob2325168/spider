package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"io"
	"os"
)

type Plugin = zapcore.Core

// NewLogger Option有些是无法覆盖的
// 默认的情况下，会打印调用的文件与行号，且只有当日志等级在DPanic等级之上时，才输出函数的堆栈信息
func NewLogger(plugin Plugin, options ...zap.Option) *zap.Logger {
	return zap.New(plugin, append(DefaultOption(), options...)...)
}

func NewPlugin(writer zapcore.WriteSyncer, enabler zapcore.LevelEnabler) Plugin {
	return zapcore.NewCore(DefaultEncoder(), writer, enabler)
}

func NewStdoutPlugin(enabler zapcore.LevelEnabler) Plugin {
	return NewPlugin(zapcore.Lock(zapcore.AddSync(os.Stdout)), enabler)
}

func NewStderrPlugin(enabler zapcore.LevelEnabler) Plugin {
	return NewPlugin(zapcore.Lock(zapcore.AddSync(os.Stderr)), enabler)
}

// NewFilePlugin Lumberjack logger虽然持有File但没有暴露sync方法，所以没办法利用zap的sync特性
// 所以额外返回一个closer，需要保证在进程退出前close以保证写入的内容可以全部刷到到磁盘
func NewFilePlugin(
	filePath string, enabler zapcore.LevelEnabler) (Plugin, io.Closer) {
	var writer = DefaultLumberjackLogger()
	writer.Filename = filePath
	return NewPlugin(zapcore.AddSync(writer), enabler), writer
}
